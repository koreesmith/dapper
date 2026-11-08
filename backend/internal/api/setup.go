package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

// The setup wizard runs only while Dapper has no configuration. Because anyone who can
// reach an unconfigured instance could otherwise point it at their own directory, every
// setup request needs a one-time code printed to the server log at startup.

func newSetupToken() string {
	if v := os.Getenv("DAPPER_SETUP_CODE"); v != "" {
		return v
	}
	return generatePassword(8)
}

func (s *Server) setupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"configured": s.store.Configured()})
}

func (s *Server) setupGate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.store.Configured() {
			fail(w, http.StatusForbidden, "Dapper is already set up.")
			return
		}
		key := "setup:" + clientIP(r)
		if s.limit.blocked(key) {
			fail(w, http.StatusTooManyRequests, "Too many incorrect setup codes. Wait a few minutes and try again.")
			return
		}
		want := s.SetupToken()
		got := strings.TrimSpace(r.Header.Get("X-Setup-Code"))
		if want == "" || subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(strings.ToLower(want))) != 1 {
			s.limit.fail(key)
			fail(w, http.StatusUnauthorized, "That setup code isn't right. You'll find it in Dapper's startup log.")
			return
		}
		s.limit.reset(key)
		h(w, r)
	}
}

// candidate builds a configuration from wizard input on top of blank defaults.
func (s *Server) candidate(o settingsIO) *config.Config {
	base := *s.c()
	base.Settings = config.Settings{}
	c := s.store.Merge(applyIO(base.Settings, o))
	return c
}

// setupDiscover connects with the service account and suggests where things live.
func (s *Server) setupDiscover(w http.ResponseWriter, r *http.Request) {
	var o settingsIO
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.candidate(o)
	checks, c := s.runChecks(&config.Config{Settings: config.Settings{LDAP: cfg.LDAP}})
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "checks": checks})
		return
	}
	defer c.Close()
	ctxs, _ := c.NamingContexts()
	sugg := map[string]string{}
	if len(ctxs) > 0 {
		suffix := ctxs[0]
		sugg = map[string]string{
			"usersBaseDN":     "ou=people," + suffix,
			"groupsBaseDN":    "ou=groups," + suffix,
			"suspendedBaseDN": "ou=suspended," + suffix,
			"adminGroupDN":    "cn=dapper-admins,ou=groups," + suffix,
		}
	}
	exists := map[string]bool{}
	for k, dn := range sugg {
		exists[k], _ = c.Exists(dn)
	}
	var det unixDetection
	if len(ctxs) > 0 {
		det = detectUnix(c, ctxs[0])
	}
	writeJSON(w, map[string]any{"ok": true, "checks": checks, "contexts": ctxs, "suggestions": sugg, "exists": exists, "unix": det})
}

// setupCheck validates the full candidate configuration.
func (s *Server) setupCheck(w http.ResponseWriter, r *http.Request) {
	var o settingsIO
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	checks, c := s.runChecks(s.candidate(o))
	if c != nil {
		c.Close()
	}
	writeJSON(w, map[string]any{"checks": checks, "ok": allOK(checks)})
}

type setupAdmin struct {
	Mode      string `json:"mode"` // existing | person | new
	Username  string `json:"username"`
	GivenName string `json:"givenName"`
	Surname   string `json:"sn"`
	Password  string `json:"password"`
}

func ensureOU(c *ldapclient.Client, dn string) error {
	ok, err := c.Exists(dn)
	if err != nil || ok {
		return err
	}
	attr, val := rdnParts(dn)
	if !strings.EqualFold(attr, "ou") || val == "" {
		return validationError("Dapper can only create containers named like ou=people. Create " + dn + " yourself first.")
	}
	return c.Add(ldapclient.Entry{DN: dn, Attrs: map[string][]string{"objectClass": {"organizationalUnit"}, "ou": {val}}})
}

// setupComplete applies the wizard: optionally creates the containers, the
// administrators group and a first administrator, then saves the settings.
func (s *Server) setupComplete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings         settingsIO `json:"settings"`
		CreateContainers bool       `json:"createContainers"`
		Admin            setupAdmin `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.candidate(in.Settings)
	if err := cfg.Validate(); err != nil {
		fail(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	if m := cfg.Missing(); len(m) > 0 {
		fail(w, http.StatusBadRequest, "Please fill in every required setting.")
		return
	}
	c, err := dialSvc(cfg)
	if err != nil {
		fail(w, http.StatusBadGateway, "Couldn't connect with the service account. Go back and check the server settings.")
		return
	}
	defer c.Close()

	if in.CreateContainers {
		for _, dn := range []string{cfg.Users.BaseDN, cfg.Groups.BaseDN, cfg.Users.SuspendedBaseDN} {
			if dn == "" {
				continue
			}
			if err := ensureOU(c, dn); err != nil {
				failLDAP(w, err)
				return
			}
		}
	}

	if in.Admin.Mode == "person" || in.Admin.Mode == "new" {
		if err := s.createFirstAdmin(c, cfg, in.Admin); err != nil {
			failLDAP(w, err)
			return
		}
	}

	checks, cc := s.runChecks(cfg)
	if cc != nil {
		cc.Close()
	}
	if !allOK(checks) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "Something still isn't right, so setup wasn't finished.", "checks": checks})
		return
	}
	if err := s.store.Save(cfg); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error())
		return
	}
	s.sessions.SetTimeouts(cfg.Session.IdleTimeout, cfg.Session.MaxAge)
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	audit(nil, "setup.complete", cfg.LDAP.URL)
	log.Printf("setup complete: now managed through the web UI")
	writeJSON(w, map[string]any{"ok": true, "checks": checks})
}

// createFirstAdmin makes sure the administrators group exists and has a first member.
func (s *Server) createFirstAdmin(c *ldapclient.Client, cfg *config.Config, a setupAdmin) error {
	username := strings.ToLower(strings.TrimSpace(a.Username))
	var userDN string
	if a.Mode == "new" {
		if len(a.Password) < cfg.Password.MinLength {
			return validationError("Choose a password of at least " + itoa(cfg.Password.MinLength) + " characters for the first administrator.")
		}
		attrs, err := s.personAttrs(cfg, username, map[string][]string{
			"givenName": {a.GivenName}, "sn": {a.Surname},
		})
		if err != nil {
			return err
		}
		if userDN, err = s.addPerson(c, cfg, username, attrs, a.Password); err != nil {
			return err
		}
	} else {
		dn, err := c.FindUserDN(cfg.Users.BaseDN, cfg.Users.LoginAttributes, username)
		if err != nil {
			if errors.Is(err, ldapclient.ErrInvalidCredentials) {
				return validationError("Couldn't find a person called “" + username + "” in the people container.")
			}
			return err
		}
		userDN = dn
	}

	member := cfg.Groups.MemberAttribute
	exists, err := c.Exists(cfg.Admin.GroupDN)
	if err != nil {
		return err
	}
	if exists {
		e, err := c.GetAttrs(cfg.Admin.GroupDN, []string{member})
		if err != nil {
			return err
		}
		for _, m := range e.Values(member) {
			if strings.EqualFold(m, userDN) {
				return nil
			}
		}
		return c.Modify(cfg.Admin.GroupDN, []ldapclient.Change{{Op: "add", Attr: member, Values: []string{userDN}}})
	}
	_, name := rdnParts(cfg.Admin.GroupDN)
	return c.Add(ldapclient.Entry{DN: cfg.Admin.GroupDN, Attrs: map[string][]string{
		"objectClass": {cfg.Groups.ObjectClass}, "cn": {name}, member: {userDN},
	}})
}

func itoa(n int) string { return strconv.Itoa(n) }
