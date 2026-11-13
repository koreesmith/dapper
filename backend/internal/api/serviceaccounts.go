package api

import (
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- {SSHA} is the format every LDAP server understands, used for a throwaway placeholder and as a fallback
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Service accounts: plain directory accounts that applications and machines use to look people up (sssd, Semaphore,
// Zabbix, Nextcloud and so on), so they don't need the directory manager. They live in one container
// (ou=services under the directory's base name), are not people (they don't show in the People list and get no home
// directory), and are given no rights to change anything. Whether they can read the directory is decided by the
// server's access rules; the page tests it and can add the one rule that lets them.

var serviceNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)

type svcAccount struct {
	DN          string `json:"dn"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// hashSSHA makes a salted {SSHA} hash, understood by every LDAP server.
func hashSSHA(pw string) string {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	h := sha1.Sum(append([]byte(pw), salt...)) // #nosec G401
	return "{SSHA}" + base64.StdEncoding.EncodeToString(append(h[:], salt...))
}

// directorySuffix is the base name of the directory Dapper manages, as the server reports it.
func (s *Server) directorySuffix(c *ldapclient.Client) string {
	base := strings.ToLower(s.c().Users.BaseDN)
	if ctxs, err := c.NamingContexts(); err == nil {
		for _, x := range ctxs {
			if strings.HasSuffix(base, strings.ToLower(x)) {
				return x
			}
		}
	}
	return suffixOf(s.c().Users.BaseDN)
}

func (s *Server) serviceContainer(c *ldapclient.Client) string {
	return "ou=services," + s.directorySuffix(c)
}

// isServiceAccountDN is true for an entry directly inside the container (and not the container itself).
func isServiceAccountDN(container, dn string) bool {
	d, c := strings.ToLower(strings.TrimSpace(dn)), strings.ToLower(container)
	if !strings.HasSuffix(d, ","+c) {
		return false
	}
	rdn := strings.TrimSuffix(d, ","+c)
	return strings.HasPrefix(rdn, "cn=") && !strings.Contains(rdn, ",")
}

// serviceAccountTest signs in as the account and reports what it can see. Secret values are read only to answer
// "can it see password hashes?"; they are never kept or returned.
type serviceTest struct {
	SignedIn   bool   `json:"signedIn"`
	CanRead    int    `json:"canRead"` // people it can see (up to 5 are tried)
	SeesHashes bool   `json:"seesHashes"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) testServiceAccount(dn, pw string) serviceTest {
	c, err := ldapclient.Dial(s.server())
	if err != nil {
		return serviceTest{Error: "Dapper couldn't reach the directory: " + err.Error()}
	}
	defer c.Close()
	if err := c.Bind(dn, pw); err != nil {
		return serviceTest{Error: "The account couldn't sign in with that password."}
	}
	c.KeepSecrets()
	t := serviceTest{SignedIn: true}
	es, err := c.SearchAttrs(s.c().Users.BaseDN, "(objectClass=person)", []string{"uid", "userPassword"}, 5)
	if err != nil {
		if !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) && !ldapclient.IsCode(err, ldapclient.CodeInsufficientAccess) {
			t.Error = "Dapper couldn't test reading: " + err.Error()
		}
		return t
	}
	t.CanRead = len(es)
	for _, e := range es {
		if len(e.Values("userPassword")) > 0 {
			t.SeesHashes = true
		}
	}
	return t
}

func (s *Server) serviceAccountsList(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	var out struct {
		Container       string       `json:"container"`
		ContainerExists bool         `json:"containerExists"`
		Accounts        []svcAccount `json:"accounts"`
		SearchBase      string       `json:"searchBase"`
		UsersBase       string       `json:"usersBase"`
		ServerURL       string       `json:"serverURL"`
		DapperRun       bool         `json:"dapperRun"`
		Host            string       `json:"host,omitempty"`
	}
	out.Accounts = []svcAccount{}
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		out.Container = s.serviceContainer(c)
		out.SearchBase = s.directorySuffix(c)
		ok, err := c.Exists(out.Container)
		if err != nil {
			return err
		}
		out.ContainerExists = ok
		if !ok {
			return nil
		}
		es, err := c.SearchAttrs(out.Container, "(&(objectClass=simpleSecurityObject)(objectClass=organizationalRole))", []string{"cn", "description"}, 500)
		if err != nil {
			return err
		}
		for _, e := range es {
			if isServiceAccountDN(out.Container, e.DN) {
				out.Accounts = append(out.Accounts, svcAccount{DN: e.DN, Name: first(e.Values("cn")), Description: first(e.Values("description"))})
			}
		}
		sort.Slice(out.Accounts, func(i, j int) bool { return out.Accounts[i].Name < out.Accounts[j].Name })
		return nil
	}) {
		return
	}
	out.UsersBase = s.c().Users.BaseDN
	out.ServerURL = s.c().LDAP.URL
	if s.dapperRuns() {
		out.DapperRun = true
		out.Host = s.c().LDAP.PublicHost
		if out.Host == "" {
			out.Host = nameFromCertificate(s.c().LDAP.CAFile)
		}
	}
	writeJSON(w, out)
}

type serviceInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Password    string `json:"password"` // empty: generate one
	DN          string `json:"dn"`
	Confirmed   bool   `json:"confirmed"`
}

func validServicePassword(pw string) error {
	if pw != "" && len(pw) < 12 {
		return validationError("Choose a password of at least 12 characters, or let Dapper generate one.")
	}
	return nil
}

func (s *Server) serviceAccountCreate(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in serviceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case !serviceNameRE.MatchString(in.Name):
		fail(w, http.StatusBadRequest, "Use a short name of lower-case letters, numbers, - and _, starting with a letter (for example sssd or nextcloud).")
		return
	case len(in.Description) > 200 || strings.ContainsAny(in.Description, "\r\n"):
		fail(w, http.StatusBadRequest, "Keep the description to one line of at most 200 characters.")
		return
	case !in.Confirmed:
		fail(w, http.StatusBadRequest, "Please approve what Dapper will create first.")
		return
	}
	if err := validServicePassword(in.Password); err != nil {
		failLDAP(w, err)
		return
	}
	generated := in.Password == ""
	pw := in.Password
	if generated {
		pw = generatePassword(24)
	}

	var dn, container string
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		container = s.serviceContainer(c)
		if err := ensureOU(c, container); err != nil {
			return err
		}
		dn = "cn=" + in.Name + "," + container
		if ok, err := c.Exists(dn); err != nil {
			return err
		} else if ok {
			return validationError("A service account called “" + in.Name + "” already exists.")
		}
		attrs := map[string][]string{
			"objectClass":  {"organizationalRole", "simpleSecurityObject"},
			"cn":           {in.Name},
			"userPassword": {hashSSHA(generatePassword(32))}, // a placeholder nobody knows; the real one is set next
		}
		if in.Description != "" {
			attrs["description"] = []string{in.Description}
		}
		if err := c.Add(ldapclient.Entry{DN: dn, Attrs: attrs}); err != nil {
			return err
		}
		return setServicePassword(c, dn, pw)
	}) {
		return
	}
	audit(sess, "serviceaccount.create", dn)
	out := map[string]any{"dn": dn, "name": in.Name, "container": container, "test": s.testServiceAccount(dn, pw)}
	if generated {
		out["password"] = pw // shown once
	}
	writeJSON(w, out)
}

// setServicePassword sets the password the way the server prefers (so it picks its own storage scheme), falling back
// to a plain {SSHA} value when the Password Modify operation isn't allowed.
func setServicePassword(c *ldapclient.Client, dn, pw string) error {
	if err := c.SetPassword(dn, pw); err == nil {
		return nil
	}
	return c.Modify(dn, []ldapclient.Change{{Op: "replace", Attr: "userPassword", Values: []string{hashSSHA(pw)}}})
}

// checkServiceAccount makes sure dn is a real service account inside the container, so this page can never be used to
// change anyone else's entry.
func (s *Server) checkServiceAccount(c *ldapclient.Client, dn string) error {
	if !isServiceAccountDN(s.serviceContainer(c), dn) {
		return validationError("That isn't one of your service accounts.")
	}
	e, err := c.GetAttrs(dn, []string{"objectClass"})
	if err != nil {
		if ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			return validationError("That service account no longer exists.")
		}
		return err
	}
	if !hasOC(*e, "simpleSecurityObject") {
		return validationError("That isn't one of your service accounts.")
	}
	return nil
}

func (s *Server) serviceAccountPassword(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in serviceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the new password first. Whatever uses the old one will stop working.")
		return
	}
	if err := validServicePassword(in.Password); err != nil {
		failLDAP(w, err)
		return
	}
	generated := in.Password == ""
	pw := in.Password
	if generated {
		pw = generatePassword(24)
	}
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		if err := s.checkServiceAccount(c, in.DN); err != nil {
			return err
		}
		return setServicePassword(c, in.DN, pw)
	}) {
		return
	}
	audit(sess, "serviceaccount.password", in.DN)
	out := map[string]any{"dn": in.DN, "test": s.testServiceAccount(in.DN, pw)}
	if generated {
		out["password"] = pw
	}
	writeJSON(w, out)
}

// serviceAccountTestHandler re-checks an account with a password the person types now (never stored).
func (s *Server) serviceAccountTestHandler(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	var in serviceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Password == "" {
		fail(w, http.StatusBadRequest, "Enter the account's password to test it.")
		return
	}
	if !s.withSvc(w, func(c *ldapclient.Client) error { return s.checkServiceAccount(c, in.DN) }) {
		return
	}
	writeJSON(w, map[string]any{"dn": in.DN, "test": s.testServiceAccount(in.DN, in.Password)})
}

func (s *Server) serviceAccountDelete(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn := r.URL.Query().Get("dn")
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		if err := s.checkServiceAccount(c, dn); err != nil {
			return err
		}
		return c.Delete(dn)
	}) {
		return
	}
	audit(sess, "serviceaccount.delete", dn)
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- letting service accounts read, through the server's access rules ----

var aclNumberRE = regexp.MustCompile(`^\{\d+\}`)

// insertServiceRead adds "by dn.children=<container> read" to the directory's last catch-all rule, so every service
// account can read what other signed-in accounts can. Password hashes stay hidden because it only works when an
// earlier rule already covers userPassword. It returns the new rule list and whether anything changed.
func insertServiceRead(current []string, container string) (rules []string, changed bool, err error) {
	for _, r := range current {
		rules = append(rules, strings.TrimSpace(aclNumberRE.ReplaceAllString(r, "")))
	}
	want := `dn.children="` + container + `"`
	last := -1
	for i, r := range rules {
		if strings.Contains(strings.ToLower(r), strings.ToLower(want)) {
			return rules, false, nil // already there
		}
		if r == "to *" || strings.HasPrefix(r, "to * ") {
			last = i
		}
	}
	if last < 0 {
		return nil, false, validationError("Dapper can't find the server's catch-all access rule (the one starting “to *”), so it won't guess where to add this. Add “by " + want + " read” to the rule that applies to everything, after the rule that protects userPassword.")
	}
	protected := false
	for _, r := range rules[:last] {
		if strings.Contains(strings.ToLower(r), "userpassword") {
			protected = true
		}
	}
	if !protected {
		return nil, false, validationError("No access rule before the catch-all protects password hashes (userPassword). Adding read access for service accounts there would let them read every password hash, so Dapper won't do it. Add a rule for userPassword first.")
	}
	rules[last] = "to * by " + want + " read" + strings.TrimPrefix(rules[last], "to *")
	return rules, true, nil
}
