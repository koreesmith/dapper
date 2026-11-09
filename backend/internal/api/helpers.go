package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"
	auditrec "github.com/koreesmith/dapper/backend/internal/audit"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// svc opens a connection bound as the service account. Callers must Close it.
// Directory writes and cross-user reads go through it; Dapper itself decides
// who is allowed to ask (admin group, field policy) before using it.
func (s *Server) svc() (*ldapclient.Client, error) {
	c, err := ldapclient.Dial(s.server())
	if err != nil {
		return nil, err
	}
	if err := c.Bind(s.c().LDAP.BindDN, s.c().LDAP.BindPassword); err != nil {
		c.Close()
		return nil, fmt.Errorf("service account: %w", err)
	}
	return c, nil
}

// withSvc runs fn with a service connection, translating failures to HTTP errors.
func (s *Server) withSvc(w http.ResponseWriter, fn func(c *ldapclient.Client) error) bool {
	c, err := s.svc()
	if err != nil {
		log.Printf("directory unavailable: %v", err)
		fail(w, http.StatusBadGateway, "Dapper can't reach the directory right now. Check the server settings or try again shortly.")
		return false
	}
	defer c.Close()
	if err := fn(c); err != nil {
		failLDAP(w, err)
		return false
	}
	return true
}

// failLDAP converts directory errors into plain-language messages.
func failLDAP(w http.ResponseWriter, err error) {
	var ve validationError
	if errors.As(err, &ve) {
		fail(w, http.StatusBadRequest, ve.Error())
		return
	}
	var fe forbiddenError
	if errors.As(err, &fe) {
		fail(w, http.StatusForbidden, fe.Error())
		return
	}
	code, msg := friendlyError(err)
	if code >= 500 {
		log.Printf("directory error: %v", err)
	}
	fail(w, code, msg)
}

// validationError is a problem with the caller's input, safe to show as-is.
type validationError string

func (e validationError) Error() string { return string(e) }

func friendlyError(err error) (int, string) {
	var le *ldap.Error
	if !errors.As(err, &le) {
		return http.StatusInternalServerError, "Something went wrong talking to the directory."
	}
	switch le.ResultCode {
	case ldap.LDAPResultInsufficientAccessRights:
		return http.StatusForbidden, "The directory refused the change: Dapper's service account doesn't have permission to do this. Ask your directory administrator to grant it write access."
	case ldap.LDAPResultEntryAlreadyExists:
		return http.StatusConflict, "That already exists. Choose a different username or name."
	case ldap.LDAPResultNoSuchObject:
		return http.StatusNotFound, "That item no longer exists. It may have been removed by someone else."
	case ldap.LDAPResultConstraintViolation:
		if low := strings.ToLower(le.Error()); strings.Contains(low, "non-unique") || strings.Contains(low, "not unique") {
			return http.StatusConflict, "Another account already uses one of those values (for example the same username or email), and the directory requires them to be unique."
		}
		return http.StatusBadRequest, "The directory rejected that value" + detail(le) + ". If this was a password, it may not meet the server's password policy."
	case ldap.LDAPResultInvalidAttributeSyntax, ldap.LDAPResultInvalidDNSyntax:
		return http.StatusBadRequest, "One of the values is in a format the directory doesn't accept."
	case ldap.LDAPResultObjectClassViolation:
		return http.StatusBadRequest, "A required field is missing for this kind of account" + detail(le) + "."
	case ldap.LDAPResultNotAllowedOnNonLeaf:
		return http.StatusConflict, "That item still contains other entries and can't be removed yet."
	case ldap.LDAPResultUndefinedAttributeType, ldap.LDAPResultNoSuchAttribute:
		return http.StatusBadRequest, "The directory doesn't support one of those fields" + detail(le) + "."
	case ldap.LDAPResultNamingViolation:
		return http.StatusBadRequest, "The directory refused that change because it would affect the name the account is stored under" + detail(le) + "."
	case ldap.LDAPResultUnwillingToPerform:
		return http.StatusBadRequest, "The directory declined to do that" + detail(le) + "."
	case ldap.LDAPResultBusy, ldap.LDAPResultUnavailable, ldap.ErrorNetwork:
		return http.StatusBadGateway, "The directory server is unavailable. Try again shortly."
	}
	return http.StatusInternalServerError, "The directory returned an unexpected error."
}

func detail(le *ldap.Error) string {
	if le.Err == nil {
		return ""
	}
	m := strings.TrimSpace(le.Err.Error())
	if m == "" || len(m) > 160 {
		return ""
	}
	return " (" + m + ")"
}

// auditLog is where audited actions are kept for the Activity page (nil if the data directory isn't writable).
var auditLog *auditrec.Log

// audit records one action: a line in the log and an entry on the Activity page. Never pass secrets.
func audit(sess *session.Session, action, target string, kv ...string) {
	actor := "-"
	if sess != nil {
		actor = sess.DN
	}
	detail := map[string]string{}
	extra := ""
	for i := 0; i+1 < len(kv); i += 2 {
		detail[kv[i]] = kv[i+1]
		extra += fmt.Sprintf(" %s=%q", kv[i], kv[i+1])
	}
	log.Printf("audit actor=%q action=%s target=%q%s", actor, action, target, extra)
	if len(detail) == 0 {
		detail = nil
	}
	auditLog.Add(auditrec.Event{Actor: actor, Action: action, Target: target, Detail: detail})
}

// ---- login rate limiting ----

const (
	maxFailures   = 5  // per account
	maxIPFailures = 20 // per client address: looser, since many people can share one
	failWindow    = 10 * time.Minute
)

func limitFor(key string) int {
	if strings.HasPrefix(key, "ip:") {
		return maxIPFailures
	}
	return maxFailures
}

type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{fails: map[string][]time.Time{}} }

func (l *limiter) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fails[key] {
		if now.Sub(t) < failWindow {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = keep
	}
	return keep
}

// blocked reports whether any key has hit the failure limit.
func (l *limiter) blocked(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		if len(l.prune(k, now)) >= limitFor(k) {
			return true
		}
	}
	return false
}

func (l *limiter) fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for _, k := range keys {
		l.fails[k] = append(l.prune(k, now), now)
	}
}

func (l *limiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.fails, k)
	}
}

// clientIP identifies the caller for rate limiting. When the connection comes from
// a private or loopback address (a reverse proxy on the same host or network), the
// address that proxy appended to X-Forwarded-For is used instead, so one person's
// failed sign-ins aren't counted against everyone behind the proxy. The proxy's own
// entry is the last one, so any values a client adds earlier are ignored.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if real := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(real) != nil {
				return real
			}
		}
	}
	return host
}

// ---- passwords ----

const pwAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword returns a random password with no look-alike characters.
func generatePassword(n int) string {
	b := make([]byte, n)
	for i := range b {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(len(pwAlphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = pwAlphabet[x.Int64()]
	}
	return string(b)
}

func (s *Server) checkPassword(pw string) error {
	if len(pw) < s.c().Password.MinLength {
		return validationError(fmt.Sprintf("Passwords must be at least %d characters long.", s.c().Password.MinLength))
	}
	return nil
}

// underBase reports whether dn is inside base (case-insensitive, DN-aware).
func underBase(base, dn string) bool {
	if base == "" {
		return false
	}
	b, err1 := ldap.ParseDN(base)
	d, err2 := ldap.ParseDN(dn)
	if err1 != nil || err2 != nil {
		return false
	}
	return b.AncestorOfFold(d) && !b.EqualFold(d)
}

// userDN validates a caller-supplied DN refers to an account Dapper manages.
func (s *Server) userDN(dn string) (string, error) {
	if underBase(s.c().Users.BaseDN, dn) || underBase(s.c().Users.SuspendedBaseDN, dn) {
		return dn, nil
	}
	return "", validationError("That account isn't managed by Dapper.")
}

func (s *Server) isSuspendedDN(dn string) bool {
	return underBase(s.c().Users.SuspendedBaseDN, dn)
}

// ldapEscape escapes a value for use inside an LDAP search filter.
func ldapEscape(v string) string { return ldap.EscapeFilter(v) }

// escapeRDN escapes a value for use in a DN component.
func escapeRDN(v string) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == ',' || r == '+' || r == '"' || r == '\\' || r == '<' || r == '>' || r == ';' || r == '=':
			b.WriteRune('\\')
		case i == 0 && (r == '#' || r == ' '):
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
