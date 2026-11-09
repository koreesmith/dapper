package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// settingsIO is the settings as the browser sees them. The service-account
// password is accepted but never returned (BindPasswordSet says whether one is saved).
type settingsIO struct {
	LDAP struct {
		URL             string `json:"url"`
		StartTLS        bool   `json:"startTLS"`
		SkipVerify      bool   `json:"skipVerify"`
		BindDN          string `json:"bindDN"`
		BindPassword    string `json:"bindPassword,omitempty"`
		BindPasswordSet bool   `json:"bindPasswordSet"`
	} `json:"ldap"`
	Users struct {
		BaseDN          string   `json:"baseDN"`
		LoginAttributes []string `json:"loginAttributes"`
		SuspendedBaseDN string   `json:"suspendedBaseDN"`
		ObjectClasses   []string `json:"objectClasses"`
		RDNAttribute    string   `json:"rdnAttribute"`
	} `json:"users"`
	Groups struct {
		BaseDN          string `json:"baseDN"`
		ObjectClass     string `json:"objectClass"`
		MemberAttribute string `json:"memberAttribute"`
	} `json:"groups"`
	Admin struct {
		GroupDN string `json:"groupDN"`
	} `json:"admin"`
	Password struct {
		MinLength int `json:"minLength"`
	} `json:"password"`
	Session struct {
		IdleMinutes int `json:"idleMinutes"`
		MaxHours    int `json:"maxHours"`
	} `json:"session"`
	Unix struct {
		Enabled      bool   `json:"enabled"`
		UIDMin       int    `json:"uidMin"`
		UIDMax       int    `json:"uidMax"`
		HomePattern  string `json:"homePattern"`
		DefaultShell string `json:"defaultShell"`
		Shadow       bool   `json:"shadow"`
		SSHKeys      bool   `json:"sshKeys"`
	} `json:"unix"`
}

func toIO(c *config.Config) settingsIO {
	var o settingsIO
	o.LDAP.URL, o.LDAP.StartTLS, o.LDAP.SkipVerify, o.LDAP.BindDN = c.LDAP.URL, c.LDAP.StartTLS, c.LDAP.SkipVerify, c.LDAP.BindDN
	o.LDAP.BindPasswordSet = c.LDAP.BindPassword != ""
	o.Users.BaseDN, o.Users.LoginAttributes, o.Users.SuspendedBaseDN = c.Users.BaseDN, c.Users.LoginAttributes, c.Users.SuspendedBaseDN
	o.Users.ObjectClasses, o.Users.RDNAttribute = c.Users.ObjectClasses, c.Users.RDNAttribute
	o.Groups.BaseDN, o.Groups.ObjectClass, o.Groups.MemberAttribute = c.Groups.BaseDN, c.Groups.ObjectClass, c.Groups.MemberAttribute
	o.Admin.GroupDN = c.Admin.GroupDN
	o.Password.MinLength = c.Password.MinLength
	o.Session.IdleMinutes, o.Session.MaxHours = int(c.Session.IdleTimeout/time.Minute), int(c.Session.MaxAge/time.Hour)
	o.Unix.Enabled, o.Unix.UIDMin, o.Unix.UIDMax = c.Unix.Enabled, c.Unix.UIDMin, c.Unix.UIDMax
	o.Unix.HomePattern, o.Unix.DefaultShell, o.Unix.Shadow, o.Unix.SSHKeys = c.Unix.HomePattern, c.Unix.DefaultShell, c.Unix.Shadow, c.Unix.SSHKeys
	return o
}

func clean(list []string) []string {
	var out []string
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// applyIO overlays what the browser sent onto base. Empty optional fields fall back to defaults.
func applyIO(base config.Settings, o settingsIO) config.Settings {
	s := base
	s.LDAP.URL, s.LDAP.StartTLS, s.LDAP.SkipVerify = strings.TrimSpace(o.LDAP.URL), o.LDAP.StartTLS, o.LDAP.SkipVerify
	s.LDAP.BindDN, s.LDAP.BindPassword = strings.TrimSpace(o.LDAP.BindDN), o.LDAP.BindPassword
	s.Users.BaseDN, s.Users.SuspendedBaseDN = strings.TrimSpace(o.Users.BaseDN), strings.TrimSpace(o.Users.SuspendedBaseDN)
	s.Users.LoginAttributes, s.Users.ObjectClasses = clean(o.Users.LoginAttributes), clean(o.Users.ObjectClasses)
	s.Users.RDNAttribute = strings.TrimSpace(o.Users.RDNAttribute)
	s.Groups.BaseDN, s.Groups.ObjectClass = strings.TrimSpace(o.Groups.BaseDN), strings.TrimSpace(o.Groups.ObjectClass)
	s.Groups.MemberAttribute = strings.TrimSpace(o.Groups.MemberAttribute)
	s.Admin.GroupDN = strings.TrimSpace(o.Admin.GroupDN)
	s.Password.MinLength = o.Password.MinLength
	s.Session.IdleTimeout, s.Session.MaxAge = time.Duration(o.Session.IdleMinutes)*time.Minute, time.Duration(o.Session.MaxHours)*time.Hour
	s.Unix = config.Unix{
		Enabled: o.Unix.Enabled, UIDMin: o.Unix.UIDMin, UIDMax: o.Unix.UIDMax,
		HomePattern: strings.TrimSpace(o.Unix.HomePattern), DefaultShell: strings.TrimSpace(o.Unix.DefaultShell),
		Shadow: o.Unix.Shadow, SSHKeys: o.Unix.SSHKeys,
	}
	return s
}

// check is one line of the "is this configuration usable?" report.
type check struct {
	Key     string `json:"key"`
	OK      bool   `json:"ok"`
	Warn    bool   `json:"warn,omitempty"`
	Message string `json:"message"`
	Missing string `json:"missing,omitempty"` // a container that doesn't exist yet and could be created
}

func allOK(cs []check) bool {
	for _, c := range cs {
		if !c.OK {
			return false
		}
	}
	return true
}

func rdnParts(dn string) (attr, value string) {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) == 0 || len(d.RDNs[0].Attributes) == 0 {
		return "", ""
	}
	return d.RDNs[0].Attributes[0].Type, d.RDNs[0].Attributes[0].Value
}

func dialSvc(cfg *config.Config) (*ldapclient.Client, error) {
	c, err := ldapclient.Dial(ldapclient.Server{URL: cfg.LDAP.URL, StartTLS: cfg.LDAP.StartTLS, SkipVerify: cfg.LDAP.SkipVerify})
	if err != nil {
		return nil, err
	}
	if err := c.Bind(cfg.LDAP.BindDN, cfg.LDAP.BindPassword); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// runChecks tries the candidate configuration against the directory. If the
// service account could connect it also returns the open connection (the
// caller closes it).
func (s *Server) runChecks(cfg *config.Config) ([]check, *ldapclient.Client) {
	if cfg.LDAP.URL == "" || cfg.LDAP.BindDN == "" || cfg.LDAP.BindPassword == "" {
		return []check{{Key: "connect", Message: "Enter the server address and the service account's name and password."}}, nil
	}
	c, err := dialSvc(cfg)
	if err != nil {
		msg := "Couldn't reach the server at " + cfg.LDAP.URL + ". Check the address, port and network access."
		if strings.Contains(err.Error(), "starttls") {
			msg = "The server didn't accept StartTLS. Check the address or turn StartTLS off."
		}
		if err == ldapclient.ErrInvalidCredentials {
			msg = "The server rejected the service account's name or password."
		}
		return []check{{Key: "connect", Message: msg}}, nil
	}
	out := []check{{Key: "connect", OK: true, Message: "Connected and signed in as the service account."}}
	if strings.HasPrefix(strings.ToLower(cfg.LDAP.URL), "ldap://") && !cfg.LDAP.StartTLS {
		out = append(out, check{Key: "encryption", OK: true, Warn: true,
			Message: "This connection isn't encrypted, so the service account's password and everyone's sign-in passwords cross the network in clear text. Use ldaps:// or turn on StartTLS unless the directory is on this same machine."})
	}

	container := func(key, label, dn string, optional bool) {
		if dn == "" {
			return
		}
		ok, err := c.Exists(dn)
		switch {
		case err != nil && ldapclient.IsCode(err, ldapclient.CodeInsufficientAccess):
			out = append(out, check{Key: key, Message: "The service account isn't allowed to read " + dn + "."})
		case err != nil:
			out = append(out, check{Key: key, Message: "Couldn't check " + dn + ": it may not be a valid name."})
		case !ok:
			out = append(out, check{Key: key, Message: label + " “" + dn + "” doesn't exist yet.", Missing: dn})
		default:
			out = append(out, check{Key: key, OK: true, Message: label + " found."})
		}
	}
	container("people", "The people container", cfg.Users.BaseDN, false)
	container("groups", "The groups container", cfg.Groups.BaseDN, true)
	container("suspended", "The suspended-people container", cfg.Users.SuspendedBaseDN, true)

	if cfg.Unix.Enabled {
		out = append(out, unixSchemaCheck(c, cfg))
	}

	if cfg.Admin.GroupDN == "" {
		out = append(out, check{Key: "admin", Message: "Choose the group whose members are administrators."})
		return out, c
	}
	e, err := c.GetAttrs(cfg.Admin.GroupDN, []string{cfg.Groups.MemberAttribute})
	switch {
	case err != nil && ldapclient.IsCode(err, ldapclient.CodeNoSuchObject):
		out = append(out, check{Key: "admin", Message: "The administrators group “" + cfg.Admin.GroupDN + "” doesn't exist."})
	case err != nil:
		out = append(out, check{Key: "admin", Message: "Couldn't read the administrators group."})
	default:
		n := 0
		for _, m := range e.Values(cfg.Groups.MemberAttribute) {
			if !strings.EqualFold(m, "cn=dapper-placeholder,"+cfg.Groups.BaseDN) {
				n++
			}
		}
		if n == 0 {
			out = append(out, check{Key: "admin", OK: true, Warn: true, Message: "The administrators group exists but has no members, so nobody could sign in as an administrator."})
		} else {
			out = append(out, check{Key: "admin", OK: true, Message: fmt.Sprintf("Administrators group found (%d member%s).", n, map[bool]string{true: "", false: "s"}[n == 1])})
		}
	}
	return out, c
}

func (s *Server) fieldsPayload() []policy.Field { return s.catalog() }

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	cfg := s.c()
	checks, c := s.runChecks(cfg)
	if c != nil {
		c.Close()
	}
	writeJSON(w, map[string]any{
		"settings": toIO(cfg), "locked": s.store.Locked(), "checks": checks, "fields": s.fieldsPayload(),
	})
}

func (s *Server) decodeSettings(w http.ResponseWriter, r *http.Request) (*config.Config, bool) {
	var o settingsIO
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return nil, false
	}
	return s.store.Merge(applyIO(s.c().Settings, o)), true
}

// checkSettings tries a candidate configuration without saving it.
func (s *Server) checkSettings(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	cfg, ok := s.decodeSettings(w, r)
	if !ok {
		return
	}
	checks, c := s.runChecks(cfg)
	if c != nil {
		c.Close()
	}
	writeJSON(w, map[string]any{"checks": checks, "ok": allOK(checks)})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	cfg, ok := s.decodeSettings(w, r)
	if !ok {
		return
	}
	if err := cfg.Validate(); err != nil {
		fail(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	if m := cfg.Missing(); len(m) > 0 {
		fail(w, http.StatusBadRequest, "Please fill in all the required settings.")
		return
	}
	checks, c := s.runChecks(cfg)
	if c != nil {
		defer c.Close()
	}
	if !allOK(checks) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{
			"error": "These settings didn't pass the connection check, so they weren't saved. Nothing has changed.", "checks": checks,
		})
		return
	}
	// Never let a change lock the person making it out of the admin screens.
	isAdmin, err := c.IsMember(cfg.Admin.GroupDN, sess.DN)
	if err != nil || !isAdmin || !(underBase(cfg.Users.BaseDN, sess.DN) || underBase(cfg.Users.SuspendedBaseDN, sess.DN)) {
		fail(w, http.StatusBadRequest, "These settings would stop you from signing in as an administrator (you'd no longer be in the administrators group or the people container), so they weren't saved.")
		return
	}
	if err := s.store.Save(cfg); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error())
		return
	}
	s.sessions.SetTimeouts(cfg.Session.IdleTimeout, cfg.Session.MaxAge)
	audit(sess, "settings.update", "settings")
	writeJSON(w, map[string]any{"settings": toIO(cfg), "locked": s.store.Locked(), "checks": checks})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *Server) putFieldPolicy(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		Fields []policy.Field `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if err := s.policy.Update(in.Fields); err != nil {
		fail(w, http.StatusBadRequest, "Couldn't save the settings: "+err.Error())
		return
	}
	audit(sess, "settings.fields.update", "policy")
	writeJSON(w, map[string]any{"fields": s.policy.Fields()})
}

// unixSchemaCheck confirms the server knows the object classes Unix accounts need.
func unixSchemaCheck(c *ldapclient.Client, cfg *config.Config) check {
	need := []string{"posixAccount", "posixGroup"}
	if cfg.Unix.Shadow {
		need = append(need, "shadowAccount")
	}
	if cfg.Unix.SSHKeys {
		need = append(need, "ldapPublicKey")
	}
	have, err := c.ObjectClassNames()
	if err != nil || len(have) == 0 {
		return check{Key: "unix", OK: true, Warn: true, Message: "Couldn't read the server's schema to confirm it supports Unix accounts."}
	}
	if missing := missingClasses(have, need); len(missing) > 0 {
		hint := ""
		for _, m := range missing {
			if m == "ldapPublicKey" {
				hint = " (SSH keys need the openssh-lpk schema; turn SSH keys off if you don't use it)"
			}
		}
		return check{Key: "unix", Message: "This server's schema doesn't define: " + strings.Join(missing, ", ") + "." + hint}
	}
	return check{Key: "unix", OK: true, Message: "The server supports Unix accounts (" + strings.Join(need, ", ") + ")."}
}

// missingClasses returns the needed object classes the schema doesn't define.
func missingClasses(have map[string]bool, need []string) []string {
	var missing []string
	for _, n := range need {
		if !have[strings.ToLower(n)] {
			missing = append(missing, n)
		}
	}
	return missing
}

// createContainer creates one of the configured containers (people, groups or
// suspended people) that is missing. Only those exact names may be created, and
// only after the admin confirmed it in the UI.
func (s *Server) createContainer(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		DN string `json:"dn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.c()
	allowed := false
	for _, dn := range []string{cfg.Users.BaseDN, cfg.Groups.BaseDN, cfg.Users.SuspendedBaseDN} {
		if dn != "" && strings.EqualFold(dn, in.DN) {
			allowed = true
		}
	}
	if !allowed {
		fail(w, http.StatusBadRequest, "Dapper only creates the containers named in its settings.")
		return
	}
	if s.withSvc(w, func(c *ldapclient.Client) error { return ensureOU(c, in.DN) }) {
		audit(sess, "settings.container.create", in.DN)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}
