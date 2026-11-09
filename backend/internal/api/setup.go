package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sort"
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
	var containers []string
	if len(ctxs) > 0 {
		containers = organizationalUnits(c, ctxs[0])
		sugg = suggestContainers(ctxs[0], containers)
	}
	exists := map[string]bool{}
	for k, dn := range sugg {
		exists[k], _ = c.Exists(dn)
	}
	var det unixDetection
	if len(ctxs) > 0 {
		det = detectUnix(c, ctxs[0])
	}
	// Which Unix-related object classes the server's schema supports, so the wizard
	// never offers something the server can't do. Left out if the schema can't be read.
	var schema map[string]bool
	if have, err := c.ObjectClassNames(); err == nil && len(have) > 0 {
		schema = map[string]bool{}
		for _, n := range []string{"posixAccount", "posixGroup", "shadowAccount", "ldapPublicKey"} {
			schema[n] = have[strings.ToLower(n)]
		}
	}
	writeJSON(w, map[string]any{"ok": true, "checks": checks, "contexts": ctxs, "suggestions": sugg, "exists": exists, "unix": det, "schema": schema, "containers": containers})
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
		Confirmed        bool       `json:"confirmed"` // the person approved the changes Dapper will make
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
	// Nothing is created in the directory without the person's explicit approval.
	if (in.CreateContainers || in.Admin.Mode == "person" || in.Admin.Mode == "new") && !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the changes Dapper will make to your directory before finishing setup.")
		return
	}
	checks, err := s.finishSetup(cfg, in.CreateContainers, in.Admin)
	var inc setupIncomplete
	var saveErr setupSaveError
	switch {
	case errors.As(err, &inc):
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": inc.Error(), "checks": inc.checks})
	case errors.As(err, &saveErr):
		fail(w, http.StatusInternalServerError, capitalize(saveErr.Error())+".")
	case errors.Is(err, errSetupConnect):
		fail(w, http.StatusBadGateway, "Couldn't connect with the service account. Go back and check the server settings.")
	case err != nil:
		failLDAP(w, err)
	default:
		writeJSON(w, map[string]any{"ok": true, "checks": checks})
	}
}

var errSetupConnect = errors.New("couldn't connect with the service account")

type setupSaveError struct{ error }

func (e setupSaveError) Error() string { return "couldn't save the settings: " + e.error.Error() }

// setupIncomplete means the final checks still found something wrong, so nothing was saved.
type setupIncomplete struct{ checks []check }

func (setupIncomplete) Error() string {
	return "Something still isn't right, so setup wasn't finished."
}

// finishSetup creates what is missing in the directory (containers, administrators group, first
// administrator), re-checks everything, and only then saves the settings and ends setup. The caller has
// already validated cfg and had the person approve the changes.
func (s *Server) finishSetup(cfg *config.Config, createContainers bool, admin setupAdmin) ([]check, error) {
	c, err := dialSvc(cfg)
	if err != nil {
		return nil, errSetupConnect
	}
	defer c.Close()

	if createContainers {
		for _, dn := range []string{cfg.Users.BaseDN, cfg.Groups.BaseDN, cfg.Users.SuspendedBaseDN} {
			if dn == "" {
				continue
			}
			if err := ensureOU(c, dn); err != nil {
				return nil, err
			}
		}
	}
	if admin.Mode == "person" || admin.Mode == "new" {
		if err := s.createFirstAdmin(c, cfg, admin); err != nil {
			return nil, err
		}
	}

	checks, cc := s.runChecks(cfg)
	if cc != nil {
		cc.Close()
	}
	if !allOK(checks) {
		return nil, setupIncomplete{checks}
	}
	if err := s.store.Save(cfg); err != nil {
		return nil, setupSaveError{err}
	}
	s.sessions.SetTimeouts(cfg.Session.IdleTimeout, cfg.Session.MaxAge)
	s.setupMu.Lock()
	s.setupToken = ""
	s.setupMu.Unlock()
	audit(nil, "setup.complete", cfg.LDAP.URL)
	log.Printf("setup complete: now managed through the web UI")
	return checks, nil
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
		if userDN, err = s.addPerson(c, cfg, username, attrs, a.Password, ""); err != nil {
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

// organizationalUnits lists the ou= containers directly below suffix.
func organizationalUnits(c *ldapclient.Client, suffix string) []string {
	es, err := c.Children(suffix)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		for _, oc := range e.Values("objectClass") {
			if strings.EqualFold(oc, "organizationalUnit") {
				out = append(out, e.DN)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// suggestContainers picks, from what really exists, the likely people, groups and
// suspended-people containers by their usual names, falling back to conventional
// names (to be created) when none is found.
func suggestContainers(suffix string, existing []string) map[string]string {
	names := map[string][]string{
		"usersBaseDN":     {"people", "users", "user", "accounts", "employees", "staff"},
		"groupsBaseDN":    {"groups", "group"},
		"suspendedBaseDN": {"suspended", "disabled", "inactive", "locked"},
	}
	fallback := map[string]string{"usersBaseDN": "people", "groupsBaseDN": "groups", "suspendedBaseDN": "suspended"}
	sugg := map[string]string{}
	for key, want := range names {
		for _, w := range want {
			for _, dn := range existing {
				if _, v := rdnParts(dn); strings.EqualFold(v, w) {
					sugg[key] = dn
					break
				}
			}
			if sugg[key] != "" {
				break
			}
		}
		if sugg[key] == "" {
			sugg[key] = "ou=" + fallback[key] + "," + suffix
		}
	}
	sugg["adminGroupDN"] = "cn=dapper-admins," + sugg["groupsBaseDN"]
	return sugg
}

// setupGroups lists the groups that already exist in the chosen groups container,
// and guesses whether the directory uses member or uniqueMember.
func (s *Server) setupGroups(w http.ResponseWriter, r *http.Request) {
	var o settingsIO
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.candidate(o)
	type group struct {
		DN      string `json:"dn"`
		Name    string `json:"name"`
		Members int    `json:"members"`
		Class   string `json:"class"`
	}
	out := []group{}
	counts := map[string]int{}
	if cfg.Groups.BaseDN != "" {
		c, err := dialSvc(cfg)
		if err != nil {
			fail(w, http.StatusBadGateway, "Couldn't connect with the service account.")
			return
		}
		defer c.Close()
		es, err := c.SearchAttrs(cfg.Groups.BaseDN, "(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames))",
			[]string{"cn", "member", "uniqueMember", "objectClass"}, 500)
		if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			failLDAP(w, err)
			return
		}
		for _, e := range es {
			g := group{DN: e.DN, Name: first(e.Values("cn")), Class: "groupOfNames"}
			attr := "member"
			for _, oc := range e.Values("objectClass") {
				if strings.EqualFold(oc, "groupOfUniqueNames") {
					g.Class, attr = "groupOfUniqueNames", "uniqueMember"
				}
			}
			counts[g.Class]++
			g.Members = len(e.Values(attr))
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	suggest := map[string]string{}
	if counts["groupOfUniqueNames"] > counts["groupOfNames"] {
		suggest = map[string]string{"objectClass": "groupOfUniqueNames", "memberAttribute": "uniqueMember"}
	} else if counts["groupOfNames"] > 0 {
		suggest = map[string]string{"objectClass": "groupOfNames", "memberAttribute": "member"}
	}
	writeJSON(w, map[string]any{"groups": out, "suggest": suggest})
}

// setupPeople finds existing people (for choosing the first administrator).
func (s *Server) setupPeople(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings settingsIO `json:"settings"`
		Q        string     `json:"q"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.candidate(in.Settings)
	q := strings.TrimSpace(in.Q)
	type person struct {
		DN       string `json:"dn"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	out := []person{}
	if len(q) >= 2 && cfg.Users.BaseDN != "" {
		c, err := dialSvc(cfg)
		if err != nil {
			fail(w, http.StatusBadGateway, "Couldn't connect with the service account.")
			return
		}
		defer c.Close()
		e := ldapEscape(q)
		es, err := c.SearchAttrs(cfg.Users.BaseDN, "(&(objectClass=person)(|(uid=*"+e+"*)(cn=*"+e+"*)(mail=*"+e+"*)))",
			[]string{"uid", "cn", "displayName"}, 8)
		if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			failLDAP(w, err)
			return
		}
		for _, p := range es {
			if u := first(p.Values("uid")); u != "" {
				name := first(p.Values("displayName"))
				if name == "" {
					name = first(p.Values("cn"))
				}
				out = append(out, person{DN: p.DN, Username: u, Name: name})
			}
		}
	}
	writeJSON(w, map[string]any{"people": out})
}
