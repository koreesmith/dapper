// Package api exposes the HTTP/JSON interface used by the web UI.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	auditrec "github.com/koreesmith/dapper/backend/internal/audit"
	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Version is the build's version, set from main.
var Version = "dev"

const cookieName = "dapper_session"

type Server struct {
	store    *config.Store
	policy   *policy.Policy
	sessions *session.Store
	limit    *limiter

	setupMu    sync.Mutex
	setupToken string // required for the setup wizard while Dapper is unconfigured
}

func New(store *config.Store, pol *policy.Policy) *Server {
	cfg := store.Get()
	s := &Server{
		store:    store,
		policy:   pol,
		limit:    newLimiter(),
		sessions: session.NewStore(cfg.Session.IdleTimeout, cfg.Session.MaxAge),
	}
	if !store.Configured() {
		s.setupToken = newSetupToken()
	}
	if l, err := auditrec.Open(cfg.DataDir); err != nil {
		log.Printf("the activity record couldn't be opened (%v); actions are still written to this log", err)
	} else {
		auditLog = l
	}
	return s
}

// c returns the current configuration snapshot.
func (s *Server) c() *config.Config { return s.store.Get() }

// server returns how to reach the directory, from the current configuration.
func (s *Server) server() ldapclient.Server {
	c := s.c()
	return ldapclient.Server{URL: c.LDAP.URL, StartTLS: c.LDAP.StartTLS, SkipVerify: c.LDAP.SkipVerify}
}

// SetupToken is the code needed to run the setup wizard, or "" once configured.
func (s *Server) SetupToken() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupToken
}

type handler func(http.ResponseWriter, *http.Request, *session.Session)

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.user(s.me))

	mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/setup/discover", s.setupGate(s.setupDiscover))
	mux.HandleFunc("POST /api/setup/check", s.setupGate(s.setupCheck))
	mux.HandleFunc("POST /api/setup/groups", s.setupGate(s.setupGroups))
	mux.HandleFunc("POST /api/setup/people", s.setupGate(s.setupPeople))
	mux.HandleFunc("POST /api/setup/complete", s.setupGate(s.setupComplete))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "version": Version})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]string{"version": Version}) })

	// Self-service: any signed-in user, limited to their own account and the field policy.
	mux.HandleFunc("GET /api/me/profile", s.user(s.getProfile))
	mux.HandleFunc("PATCH /api/me/profile", s.user(s.updateProfile))
	mux.HandleFunc("POST /api/me/password", s.user(s.changeOwnPassword))
	mux.HandleFunc("GET /api/me/password-policy", s.user(s.myPasswordPolicy))
	mux.HandleFunc("GET /api/photo", s.user(s.getPhoto))
	mux.HandleFunc("PUT /api/me/photo", s.user(s.putOwnPhoto))
	mux.HandleFunc("DELETE /api/me/photo", s.user(s.deleteOwnPhoto))
	mux.HandleFunc("PUT /api/admin/user/photo", s.admin(s.putUserPhoto))
	mux.HandleFunc("DELETE /api/admin/user/photo", s.admin(s.deleteUserPhoto))

	// Administration of people, groups and settings.
	mux.HandleFunc("GET /api/admin/users", s.admin(s.listUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("GET /api/admin/ous", s.admin(s.listOUs))
	mux.HandleFunc("POST /api/admin/ous", s.admin(s.createOU))
	mux.HandleFunc("POST /api/admin/ous/rename", s.admin(s.renameOU))
	mux.HandleFunc("DELETE /api/admin/ou", s.admin(s.deleteOU))
	mux.HandleFunc("POST /api/admin/user/move", s.admin(s.movePerson))
	mux.HandleFunc("POST /api/admin/users/import", s.admin(s.importUsers))
	mux.HandleFunc("POST /api/admin/users/bulk", s.admin(s.bulkUsers))
	mux.HandleFunc("GET /api/admin/user", s.admin(s.getUser))
	mux.HandleFunc("PATCH /api/admin/user", s.admin(s.updateUser))
	mux.HandleFunc("DELETE /api/admin/user", s.admin(s.deleteUser))
	mux.HandleFunc("POST /api/admin/user/suspend", s.admin(s.setSuspended(true)))
	mux.HandleFunc("POST /api/admin/user/restore", s.admin(s.setSuspended(false)))
	mux.HandleFunc("POST /api/admin/user/password", s.admin(s.resetPassword))
	mux.HandleFunc("GET /api/admin/groups", s.admin(s.listGroups))
	mux.HandleFunc("POST /api/admin/groups", s.admin(s.createGroup))
	mux.HandleFunc("GET /api/admin/group", s.admin(s.getGroup))
	mux.HandleFunc("DELETE /api/admin/group", s.admin(s.deleteGroup))
	mux.HandleFunc("GET /api/admin/audit", s.admin(s.listAudit))
	mux.HandleFunc("GET /api/admin/groups/check", s.admin(s.checkGroups))
	mux.HandleFunc("POST /api/admin/group/gid", s.admin(s.groupGID))
	mux.HandleFunc("POST /api/admin/user/gid", s.admin(s.personGID))
	mux.HandleFunc("POST /api/admin/group/members", s.admin(s.changeMember(true)))
	mux.HandleFunc("DELETE /api/admin/group/members", s.admin(s.changeMember(false)))
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.getSettings))
	mux.HandleFunc("POST /api/admin/settings/check", s.admin(s.checkSettings))
	mux.HandleFunc("POST /api/admin/settings/containers", s.admin(s.createContainer))
	mux.HandleFunc("GET /api/admin/server/config", s.admin(s.serverGet))
	mux.HandleFunc("POST /api/admin/server/preview", s.admin(s.serverPreview))
	mux.HandleFunc("POST /api/admin/server/apply", s.admin(s.serverApply))
	mux.HandleFunc("GET /api/admin/overlays", s.admin(s.overlaysGet))
	mux.HandleFunc("POST /api/admin/overlays/unlock", s.admin(s.overlaysUnlock))
	mux.HandleFunc("POST /api/admin/overlays/lock", s.admin(s.overlaysLock))
	mux.HandleFunc("POST /api/admin/overlays/preview", s.admin(s.overlaysPreview))
	mux.HandleFunc("POST /api/admin/overlays/apply", s.admin(s.overlaysApply))
	mux.HandleFunc("POST /api/admin/overlays/memberof/refresh", s.admin(s.overlaysRefreshMemberOf))
	mux.HandleFunc("GET /api/admin/overlays/backup", s.admin(s.overlaysBackup))
	mux.HandleFunc("PUT /api/admin/settings", s.admin(s.putSettings))
	mux.HandleFunc("PUT /api/admin/settings/fields", s.admin(s.putFieldPolicy))

	// Admin-only directory access. Enforced here, not just hidden in the UI.
	mux.HandleFunc("GET /api/admin/children", s.admin(s.children))
	mux.HandleFunc("GET /api/admin/entry", s.admin(s.getEntry))
	mux.HandleFunc("GET /api/admin/search", s.admin(s.search))
	mux.HandleFunc("POST /api/admin/entry", s.admin(s.addEntry))
	mux.HandleFunc("PATCH /api/admin/entry", s.admin(s.modifyEntry))
	mux.HandleFunc("DELETE /api/admin/entry", s.admin(s.deleteEntry))

	if static != nil {
		mux.Handle("/", static)
	}
	return secure(mux)
}

// secure adds security headers and a CSRF guard: state-changing requests must
// carry a custom header that a cross-site form cannot set.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "dapper" {
			fail(w, http.StatusForbidden, "forbidden")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) user(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess := s.sessions.Get(c.Value)
		if sess == nil {
			fail(w, http.StatusUnauthorized, "not signed in")
			return
		}
		h(w, r, sess)
	}
}

func (s *Server) admin(h handler) http.HandlerFunc {
	return s.user(func(w http.ResponseWriter, r *http.Request, sess *session.Session) {
		if !sess.IsAdmin {
			fail(w, http.StatusForbidden, "administrator access required")
			return
		}
		h(w, r, sess)
	})
}

const loginFailed = "Incorrect username or password."

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username == "" || in.Password == "" {
		fail(w, http.StatusUnauthorized, loginFailed)
		return
	}
	in.Username = strings.TrimSpace(in.Username)

	if !s.store.Configured() {
		fail(w, http.StatusServiceUnavailable, "Dapper hasn't been set up yet. An administrator needs to finish setup first.")
		return
	}
	ipKey, userKey := "ip:"+clientIP(r), "user:"+strings.ToLower(in.Username)
	if s.limit.blocked(ipKey, userKey) {
		audit(nil, "login.blocked", in.Username, "ip", clientIP(r))
		w.Header().Set("Retry-After", "600")
		fail(w, http.StatusTooManyRequests, "Too many failed sign-in attempts. Wait a few minutes and try again.")
		return
	}
	failed := func() {
		s.limit.fail(ipKey, userKey)
		audit(nil, "login.failed", in.Username, "ip", clientIP(r)) // the username typed, never the password
		fail(w, http.StatusUnauthorized, loginFailed)
	}

	// 1. Look up the user's DN and admin status with the service account.
	svc, err := ldapclient.Dial(s.server())
	if err != nil {
		log.Printf("login: %v", err)
		fail(w, http.StatusBadGateway, "The directory server is unavailable. Try again shortly.")
		return
	}
	defer svc.Close()
	if err := svc.Bind(s.c().LDAP.BindDN, s.c().LDAP.BindPassword); err != nil {
		log.Printf("login: service account bind failed: %v", err)
		fail(w, http.StatusBadGateway, "Dapper cannot reach the directory (service account problem). Contact your administrator.")
		return
	}
	dn, err := svc.FindUserDN(s.c().Users.BaseDN, s.c().Users.LoginAttributes, in.Username)
	if err != nil {
		if !errors.Is(err, ldapclient.ErrInvalidCredentials) {
			log.Printf("login: user lookup: %v", err)
		}
		failed()
		return
	}
	if s.isSuspendedDN(dn) { // the suspended container may sit under the people container
		failed()
		return
	}
	isAdmin, err := svc.IsMember(s.c().Admin.GroupDN, dn)
	if err != nil {
		log.Printf("login: admin group check: %v", err)
		fail(w, http.StatusBadGateway, "Could not determine your access level. Contact your administrator.")
		return
	}

	// 2. Verify the password by binding as the user on a fresh connection,
	// which is then kept for the session so the directory's ACLs apply.
	conn, err := ldapclient.Dial(s.server())
	if err != nil {
		fail(w, http.StatusBadGateway, "The directory server is unavailable. Try again shortly.")
		return
	}
	if err := conn.Bind(dn, in.Password); err != nil {
		conn.Close()
		if !errors.Is(err, ldapclient.ErrInvalidCredentials) {
			log.Printf("login: user bind: %v", err)
		}
		failed()
		return
	}

	s.limit.reset(ipKey, userKey)
	sess := s.sessions.Create(dn, isAdmin, conn)
	audit(sess, "login", dn)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: sess.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure:  s.secureCookie(r),
		Expires: time.Now().Add(s.c().Session.MaxAge),
	})
	s.me(w, r, sess)
}

func (s *Server) secureCookie(r *http.Request) bool {
	if s.c().Session.SecureCookie != nil {
		return *s.c().Session.SecureCookie
	}
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	out := map[string]any{
		"dn": sess.DN, "isAdmin": sess.IsAdmin, "name": sess.DN,
		"features": map[string]any{
			"groups":            s.groupsEnabled(),
			"suspendByMove":     s.c().Users.SuspendedBaseDN != "",
			"passwordMinLength": s.c().Password.MinLength,
			"unix":              s.c().Unix.Enabled,
			"sshKeys":           s.c().Unix.Enabled && s.c().Unix.SSHKeys,
			"homePattern":       s.c().Unix.HomePattern,
			"defaultShell":      s.c().Unix.DefaultShell,
		},
	}
	if e, err := sess.Conn.Get(sess.DN); err == nil {
		for _, a := range []string{"displayName", "cn", "uid"} {
			if v := e.Attrs[a]; len(v) > 0 {
				out["name"] = v[0]
				break
			}
		}
	}
	writeJSON(w, out)
}

// rootDN is the top of the directory tree shown in the Advanced browser: the
// users container's domain suffix (from its first dc= component), if any.
func (s *Server) rootDN() string { return suffixOf(s.c().Users.BaseDN) }

// The Advanced (raw LDAP) handlers run as the service account after the
// admin check, and every change is audited.

func (s *Server) children(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	dn := r.URL.Query().Get("dn")
	if dn == "" {
		dn = s.rootDN()
	}
	var res []ldapclient.Entry
	if s.withSvc(w, func(c *ldapclient.Client) (err error) { res, err = c.Children(dn); return }) {
		writeJSON(w, res)
	}
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	var res *ldapclient.Entry
	if s.withSvc(w, func(c *ldapclient.Client) (err error) { res, err = c.Get(r.URL.Query().Get("dn")); return }) {
		writeJSON(w, res)
	}
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	q := r.URL.Query()
	base := q.Get("base")
	if base == "" {
		base = s.rootDN()
	}
	var res []ldapclient.Entry
	if s.withSvc(w, func(c *ldapclient.Client) (err error) { res, err = c.Search(base, q.Get("filter"), 200); return }) {
		writeJSON(w, res)
	}
}

func (s *Server) addEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var e ldapclient.Entry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if s.withSvc(w, func(c *ldapclient.Client) error { return c.Add(e) }) {
		audit(sess, "raw.add", e.DN)
		writeJSON(w, map[string]string{"dn": e.DN})
	}
}

func (s *Server) modifyEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var ch []ldapclient.Change
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	dn := r.URL.Query().Get("dn")
	if s.withSvc(w, func(c *ldapclient.Client) error { return c.Modify(dn, ch) }) {
		audit(sess, "raw.modify", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn := r.URL.Query().Get("dn")
	if s.withSvc(w, func(c *ldapclient.Client) error { return c.Delete(dn) }) {
		audit(sess, "raw.delete", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
