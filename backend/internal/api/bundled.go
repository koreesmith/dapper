package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/overlays"
)

// Setup of a Dapper-run directory server (the openldap/ image). On its first start that server writes a
// hand-off file with its configuration login and certificate to a volume shared with Dapper. Setup uses it to
// create the directory, then removes it, and Dapper never stores the configuration login.

// handoff is the first-start file written by the directory server image.
type handoff struct {
	ConfigDN       string `json:"configDN"`
	ConfigPassword string `json:"configPassword"`
	Host           string `json:"host"`
	Certificate    string `json:"certificate"`
}

func handoffPath() string {
	dir := os.Getenv("DAPPER_HANDOFF_DIR")
	if dir == "" {
		dir = "/handoff"
	}
	return filepath.Join(dir, "bootstrap.json")
}

// bundledURL is where Dapper reaches the Dapper-run server (the compose service name).
func bundledURL() string {
	if v := os.Getenv("DAPPER_BUNDLED_URL"); v != "" {
		return v
	}
	return "ldaps://openldap:636"
}

func readHandoff() (*handoff, error) {
	b, err := os.ReadFile(handoffPath())
	if err != nil {
		return nil, err
	}
	var h handoff
	if err := json.Unmarshal(b, &h); err != nil || h.ConfigDN == "" || h.ConfigPassword == "" || h.Certificate == "" {
		return nil, errors.New("the server's first-start file isn't in the expected format")
	}
	return &h, nil
}

// caPath is where Dapper keeps the certificate it trusts for the directory.
func (s *Server) caPath() string { return filepath.Join(s.c().DataDir, "ldap-ca.pem") }

// configConn connects to the Dapper-run server as its configuration administrator.
func (s *Server) configConn(h *handoff) (*ldapclient.Client, error) {
	if err := os.MkdirAll(s.c().DataDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.caPath(), []byte(h.Certificate), 0o600); err != nil {
		return nil, err
	}
	c, err := ldapclient.Dial(ldapclient.Server{URL: bundledURL(), CAFile: s.caPath()})
	if err != nil {
		return nil, err
	}
	if err := c.Bind(h.ConfigDN, h.ConfigPassword); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// setupBundledStatus says whether a Dapper-run server is waiting to be set up.
func (s *Server) setupBundledStatus(w http.ResponseWriter, _ *http.Request) {
	h, err := readHandoff()
	if err != nil {
		msg := "Dapper can't find a directory server waiting to be set up. Either it isn't running yet, or it has already been set up."
		if !os.IsNotExist(err) {
			msg = "Dapper found the directory server's first-start file but couldn't use it: " + err.Error() + "."
		}
		writeJSON(w, map[string]any{"found": false, "message": msg})
		return
	}
	cc, err := s.configConn(h)
	if err != nil {
		writeJSON(w, map[string]any{"found": true, "reachable": false, "host": h.Host,
			"message": "The directory server's first-start file is there, but Dapper couldn't connect to the server: " + err.Error() + "."})
		return
	}
	cc.Close()
	writeJSON(w, map[string]any{"found": true, "reachable": true, "host": h.Host})
}

type bundledFeatures struct {
	MemberOf bool `json:"memberOf"`
	Refint   bool `json:"refint"`
}

type bundledRequest struct {
	Organization string          `json:"organization"`
	Domain       string          `json:"domain"`
	Settings     settingsIO      `json:"settings"` // Linux accounts, password and sign-out rules
	Admin        setupAdmin      `json:"admin"`
	Features     bundledFeatures `json:"features"`
	Confirmed    bool            `json:"confirmed"`
}

var domainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// suffixFromDomain turns example.org into dc=example,dc=org.
func suffixFromDomain(domain string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(d) {
		return "", validationError("Enter your domain name with at least two parts, for example example.org.")
	}
	parts := strings.Split(d, ".")
	for i, p := range parts {
		parts[i] = "dc=" + p
	}
	return strings.Join(parts, ","), nil
}

// bundledLayout works out the names Dapper will use for a new directory.
func bundledLayout(req bundledRequest) (suffix string, st settingsIO, err error) {
	if suffix, err = suffixFromDomain(req.Domain); err != nil {
		return
	}
	if strings.TrimSpace(req.Organization) == "" {
		err = validationError("Enter your organisation's name.")
		return
	}
	st = req.Settings
	st.LDAP.URL, st.LDAP.StartTLS, st.LDAP.SkipVerify = bundledURL(), false, false
	st.LDAP.BindDN = "cn=admin," + suffix
	st.Users.BaseDN, st.Users.SuspendedBaseDN = "ou=people,"+suffix, "ou=suspended,"+suffix
	st.Groups.BaseDN, st.Groups.ObjectClass, st.Groups.MemberAttribute = "ou=groups,"+suffix, "groupOfNames", "member"
	st.Admin.GroupDN = "cn=dapper-admins,ou=groups," + suffix
	return
}

func bundledPlan(req bundledRequest, suffix string) []string {
	plan := []string{
		"Create your directory “" + suffix + "” on the new server, with a directory manager account (cn=admin) and a generated password",
		"Create the top entry for “" + strings.TrimSpace(req.Organization) + "”",
		"Create the containers ou=people, ou=groups and ou=suspended",
		"Create the account “" + strings.ToLower(strings.TrimSpace(req.Admin.Username)) + "” as the first administrator, and the group cn=dapper-admins that makes it one",
	}
	if req.Features.MemberOf {
		plan = append(plan, "Turn on “memberOf”, so each person shows which groups they belong to")
	}
	if req.Features.Refint {
		plan = append(plan, "Turn on “referential integrity”, so deleting a person also removes them from groups")
	}
	return append(plan, "Save Dapper's settings so it manages the new directory, and trust the server's own certificate")
}

func (s *Server) setupBundledPreview(w http.ResponseWriter, r *http.Request) {
	var req bundledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	suffix, _, err := bundledLayout(req)
	if err != nil {
		failLDAP(w, err)
		return
	}
	writeJSON(w, map[string]any{"suffix": suffix, "plan": bundledPlan(req, suffix)})
}

// hashArgon2 makes a password hash the way the directory server's {ARGON2} scheme expects.
func hashArgon2(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	const m, t, p = 7168, 5, 1
	key := argon2.IDKey([]byte(pw), salt, t, m, p, 32)
	enc := base64.RawStdEncoding
	return "{ARGON2}$argon2id$v=19$m=" + strconv.Itoa(m) + ",t=" + strconv.Itoa(t) + ",p=" + strconv.Itoa(p) +
		"$" + enc.EncodeToString(salt) + "$" + enc.EncodeToString(key)
}

var dbIndexRE = regexp.MustCompile(`^olcDatabase=\{(-?\d+)\}`)

// createDatabase adds the directory's database through cn=config (or, when a database for this suffix is
// already there from an earlier attempt, only sets a new directory manager password). It returns the
// database's configuration DN.
func createDatabase(cc *ldapclient.Client, suffix, rootDN, rootPW string) (string, error) {
	dbs, err := cc.SearchAttrs("cn=config", "(objectClass=olcDatabaseConfig)", []string{"olcSuffix"}, 0)
	if err != nil {
		return "", err
	}
	next := 1
	for _, d := range dbs {
		if strings.EqualFold(first(d.Values("olcSuffix")), suffix) {
			// An earlier attempt got this far: keep the database, and give it the password shown this time.
			err := cc.Modify(d.DN, []ldapclient.Change{{Op: "replace", Attr: "olcRootPW", Values: []string{hashArgon2(rootPW)}}})
			return d.DN, err
		}
		if m := dbIndexRE.FindStringSubmatch(d.DN); m != nil {
			if n, _ := strconv.Atoi(m[1]); n >= next {
				next = n + 1
			}
		}
	}
	dn := fmt.Sprintf("olcDatabase={%d}mdb,cn=config", next)
	return dn, cc.Add(ldapclient.Entry{DN: dn, Attrs: map[string][]string{
		"objectClass":    {"olcDatabaseConfig", "olcMdbConfig"},
		"olcDatabase":    {fmt.Sprintf("{%d}mdb", next)},
		"olcDbDirectory": {"/var/lib/ldap"},
		"olcSuffix":      {suffix},
		"olcRootDN":      {rootDN},
		"olcRootPW":      {hashArgon2(rootPW)},
		// The default size limit is 10 MB, which a real directory outgrows; the file only takes the space it uses.
		"olcDbMaxSize": {"1073741824"},
		"olcDbIndex":   {"objectClass eq", "entryUUID eq", "entryCSN eq", "cn,uid,mail,sn,givenName eq,sub", "member,memberUid,uniqueMember eq", "uidNumber,gidNumber eq"},
		"olcAccess": {
			"{0}to attrs=userPassword,shadowLastChange by self write by anonymous auth by * none",
			"{1}to * by * read",
		},
	}})
}

// enableOverlays switches on the chosen overlays using the same plans the Server page uses.
func enableOverlays(cc *ldapclient.Client, dbDN, suffix string, f bundledFeatures) error {
	st := &overlays.State{DBDN: dbDN, Suffix: suffix, ModuleDN: "cn=module{0},cn=config", Overlays: map[string]*overlays.Overlay{}}
	// The server image loads these modules at first start, so nothing needs loading.
	st.Modules = []string{"memberof", "refint"}
	ctx := overlays.Context{Suffix: suffix, UsersBase: "ou=people," + suffix, GroupsBase: "ou=groups," + suffix, GroupClass: "groupOfNames", MemberAttr: "member", PlaceholderDN: "cn=placeholder,ou=groups," + suffix}
	for _, id := range []string{"memberof", "refint"} {
		if (id == "memberof" && !f.MemberOf) || (id == "refint" && !f.Refint) {
			continue
		}
		plan, err := overlays.BuildPlan(ctx, st, id, "enable", map[string]string{}, overlays.Facts{})
		if err != nil {
			return err
		}
		for _, op := range plan.Ops {
			if err := applyOp(cc, op); err != nil && !ldapclient.IsCode(err, ldapclient.CodeAlreadyExists) {
				return fmt.Errorf("turning on %s: %w", id, err)
			}
		}
	}
	return nil
}

func (s *Server) setupBundledApply(w http.ResponseWriter, r *http.Request) {
	var req bundledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	suffix, st, err := bundledLayout(req)
	if err != nil {
		failLDAP(w, err)
		return
	}
	if !req.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the changes Dapper will make before it sets up the new server.")
		return
	}
	req.Admin.Mode = "new"
	if err := validateFirstAdmin(req.Admin); err != nil {
		failLDAP(w, err)
		return
	}
	h, err := readHandoff()
	if err != nil {
		fail(w, http.StatusConflict, "Dapper can't find the new server's first-start file, so it can't take the server over. If it was already set up, use “I already have an LDAP server”.")
		return
	}
	cc, err := s.configConn(h)
	if err != nil {
		fail(w, http.StatusBadGateway, "Couldn't connect to the new directory server: "+err.Error()+".")
		return
	}
	defer cc.Close()

	managerPW := generatePassword(24)
	st.LDAP.BindPassword = managerPW
	dbDN, err := createDatabase(cc, suffix, st.LDAP.BindDN, managerPW)
	if err != nil {
		code, msg := friendlyOverlayError(err, "the directory")
		fail(w, code, "Couldn't create the directory on the new server. "+msg)
		return
	}

	// The base entry, as the directory manager, once the database answers.
	cfg := s.candidate(st)
	cfg.LDAP.CAFile = s.caPath()
	svc, err := dialSvc(cfg)
	if err != nil {
		fail(w, http.StatusBadGateway, "The new directory was created, but Dapper couldn't sign in to it: "+err.Error()+".")
		return
	}
	defer svc.Close()
	if ok, _ := svc.Exists(suffix); !ok {
		label := strings.TrimPrefix(strings.SplitN(suffix, ",", 2)[0], "dc=")
		if err := svc.Add(ldapclient.Entry{DN: suffix, Attrs: map[string][]string{
			"objectClass": {"dcObject", "organization"}, "dc": {label}, "o": {strings.TrimSpace(req.Organization)},
		}}); err != nil {
			failLDAP(w, err)
			return
		}
	}
	if err := enableOverlays(cc, dbDN, suffix, req.Features); err != nil {
		code, msg := friendlyOverlayError(err, "the overlay")
		fail(w, code, msg)
		return
	}

	// Containers, administrators, checks and saving are the same as for a server Dapper didn't create.
	if err := cfg.Validate(); err != nil {
		fail(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	checks, err := s.finishSetup(cfg, true, req.Admin)
	var inc setupIncomplete
	var saveErr setupSaveError
	switch {
	case errors.As(err, &inc):
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": inc.Error(), "checks": inc.checks})
		return
	case errors.As(err, &saveErr):
		fail(w, http.StatusInternalServerError, capitalize(saveErr.Error())+".")
		return
	case err != nil:
		failLDAP(w, err)
		return
	}
	_ = os.Remove(handoffPath()) // taken over: the configuration login lives only in this response
	audit(nil, "setup.bundled", suffix)
	writeJSON(w, map[string]any{
		"ok": true, "checks": checks, "suffix": suffix, "host": h.Host,
		"configDN": h.ConfigDN, "configPassword": h.ConfigPassword,
		"managerDN": st.LDAP.BindDN, "managerPassword": managerPW,
		"certificate": h.Certificate,
	})
}

// validateFirstAdmin checks the first administrator's details before anything is created.
func validateFirstAdmin(a setupAdmin) error {
	if strings.TrimSpace(a.Username) == "" || strings.TrimSpace(a.GivenName) == "" || strings.TrimSpace(a.Surname) == "" {
		return validationError("Enter the first administrator's username, first name and last name.")
	}
	if a.Password == "" {
		return validationError("Choose a password for the first administrator.")
	}
	return nil
}
