package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/overlays"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Migration of an existing directory into a Dapper-run server.
//
// The old server stays untouched: Dapper only reads it, as its directory manager (entered for each check or copy and
// never stored). The new server gets a directory with the same base name, so every name, group and client setting
// stays valid. The copy can be repeated until the person switches Dapper over, and the switch can be undone.

const maxMigrationEntries = 200000

// passwordSchemes the Dapper-run server can verify (its image loads the modules for these).
var supportedHashes = map[string]bool{
	"SSHA": true, "SHA": true, "SMD5": true, "MD5": true, "CRYPT": true, "CLEARTEXT": true, "ARGON2": true,
	"SSHA256": true, "SHA256": true, "SSHA384": true, "SHA384": true, "SSHA512": true, "SHA512": true,
	"PBKDF2": true, "PBKDF2-SHA1": true, "PBKDF2-SHA256": true, "PBKDF2-SHA512": true, "APR1": true,
}

// overlays Dapper can carry over (the others are reported so the person can set them up again).
var carriedOverlays = []string{"memberof", "refint", "unique", "lastbind"}

// hashScheme names the scheme of a stored password, "" when it is plain text.
func hashScheme(v string) string {
	if strings.HasPrefix(v, "{") {
		if i := strings.Index(v, "}"); i > 1 {
			return strings.ToUpper(v[1:i])
		}
	}
	return ""
}

func hasOC(e ldapclient.Entry, names ...string) bool {
	for _, v := range e.Values("objectClass") {
		for _, n := range names {
			if strings.EqualFold(v, n) {
				return true
			}
		}
	}
	return false
}

func isGroupEntry(e ldapclient.Entry) bool {
	return hasOC(e, "groupOfNames", "groupOfUniqueNames", "posixGroup")
}

func migKind(e ldapclient.Entry) string {
	switch {
	case isGroupEntry(e):
		return "groups"
	case hasOC(e, "person", "inetOrgPerson", "posixAccount"):
		return "people"
	case hasOC(e, "organizationalUnit"):
		return "units"
	}
	return "other"
}

func migDepth(dn string) int {
	if d, err := ldap.ParseDN(dn); err == nil {
		return len(d.RDNs)
	}
	return strings.Count(dn, ",") + 1
}

// orderForCopy puts parents before children and people before groups, so memberOf is complete when it is on.
func orderForCopy(es []ldapclient.Entry) []ldapclient.Entry {
	out := append([]ldapclient.Entry(nil), es...)
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := isGroupEntry(out[i]), isGroupEntry(out[j])
		if gi != gj {
			return !gi
		}
		return migDepth(out[i].DN) < migDepth(out[j].DN)
	})
	return out
}

func sameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// diffAttrs gives the changes that turn an entry with attributes have into one with attributes want.
func diffAttrs(want, have map[string][]string) []ldapclient.Change {
	low := map[string][]string{}
	for k, v := range have {
		low[strings.ToLower(k)] = v
	}
	var ch []ldapclient.Change
	wantLow := map[string]bool{}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		wantLow[strings.ToLower(k)] = true
		if !sameValues(want[k], low[strings.ToLower(k)]) {
			ch = append(ch, ldapclient.Change{Op: "replace", Attr: k, Values: want[k]})
		}
	}
	extra := make([]string, 0)
	for k := range have {
		if !wantLow[strings.ToLower(k)] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		ch = append(ch, ldapclient.Change{Op: "delete", Attr: k})
	}
	return ch
}

// unsupportedFor lists the schema an entry uses that the new server doesn't know.
func unsupportedFor(e ldapclient.Entry, classes, attrs map[string]bool) (cls, att []string) {
	for _, oc := range e.Values("objectClass") {
		if !classes[strings.ToLower(oc)] {
			cls = append(cls, oc)
		}
	}
	for k := range e.Attrs {
		base := strings.ToLower(strings.SplitN(k, ";", 2)[0])
		if base == "objectclass" {
			continue
		}
		if !attrs[base] {
			att = append(att, strings.SplitN(k, ";", 2)[0])
		}
	}
	sort.Strings(att)
	return
}

// ---- state ----

type migJob struct {
	Running  bool       `json:"running"`
	Phase    string     `json:"phase"`
	Done     int        `json:"done"`
	Total    int        `json:"total"`
	Error    string     `json:"error,omitempty"`
	Result   *migResult `json:"result,omitempty"`
	Started  time.Time  `json:"started"`
	Finished time.Time  `json:"finished"`
}

type migTarget struct {
	suffix, managerPW string
	verified          bool
}

type migState struct {
	mu     sync.Mutex
	job    migJob
	target migTarget
}

type migResult struct {
	SourceEntries   int      `json:"sourceEntries"`
	Added           int      `json:"added"`
	Updated         int      `json:"updated"`
	Unchanged       int      `json:"unchanged"`
	Skipped         int      `json:"skipped"`
	Failed          int      `json:"failed"`
	Failures        []string `json:"failures,omitempty"`
	Identical       int      `json:"identical"`
	Mismatched      int      `json:"mismatched"`
	MismatchSample  []string `json:"mismatchSample,omitempty"`
	Extras          int      `json:"extras"`
	ExtrasSample    []string `json:"extrasSample,omitempty"`
	Verified        bool     `json:"verified"`
	CarriedOverlays []string `json:"carriedOverlays,omitempty"`
}

func (s *Server) migJobSnapshot() migJob {
	s.mig.mu.Lock()
	defer s.mig.mu.Unlock()
	return s.mig.job
}

// ---- the check ----

type migUnsupported struct {
	Kind   string   `json:"kind"` // object class or attribute
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Sample []string `json:"sample"`
}

type migHash struct {
	Scheme    string `json:"scheme"`
	Count     int    `json:"count"`
	Supported bool   `json:"supported"`
}

type migCheck struct {
	Suffix        string           `json:"suffix"`
	Entries       int              `json:"entries"`
	Kinds         map[string]int   `json:"kinds"`
	Unsupported   []migUnsupported `json:"unsupported"`
	Affected      int              `json:"affectedEntries"`
	Hashes        []migHash        `json:"hashes"`
	PlainText     int              `json:"plainText"`
	Carry         []string         `json:"carryOverlays"`
	NotCarried    []string         `json:"notCarried"`
	OverlaysKnown bool             `json:"overlaysKnown"`
	Blockers      []string         `json:"blockers"`
	Warnings      []string         `json:"warnings"`
	TargetFound   bool             `json:"targetFound"`
	Host          string           `json:"host,omitempty"`
}

type migCreds struct {
	SourceDN        string `json:"sourceDN"`
	SourcePassword  string `json:"sourcePassword"`
	SkipUnsupported bool   `json:"skipUnsupported"`
	Confirmed       bool   `json:"confirmed"`
}

func (s *Server) dialSource(c migCreds) (*ldapclient.Client, error) {
	conn, err := ldapclient.Dial(s.server())
	if err != nil {
		return nil, fmt.Errorf("Dapper couldn't reach the old server: %v", err)
	}
	if err := conn.Bind(strings.TrimSpace(c.SourceDN), c.SourcePassword); err != nil {
		conn.Close()
		if errors.Is(err, ldapclient.ErrInvalidCredentials) {
			return nil, validationError("The old server didn't accept that name and password. Use its directory manager, for example cn=admin,dc=example,dc=com.")
		}
		return nil, err
	}
	conn.KeepSecrets() // password hashes are copied as they are, never shown
	return conn, nil
}

// sourceSuffix is the base name of the directory Dapper manages on the old server.
func (s *Server) sourceSuffix(c *ldapclient.Client) string {
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

type migOverlay struct {
	ID     string
	Values map[string]string
}

// sourceOverlays reads the overlays of the old server when the person has unlocked its settings.
func (s *Server) sourceOverlays(sess *session.Session) (carry []migOverlay, skip []string, aclRules int, known bool) {
	cc := sess.ConfigConn(configIdle)
	if cc == nil {
		return nil, nil, 0, false
	}
	st, err := loadOverlayState(cc, s.c().Users.BaseDN)
	if err != nil {
		return nil, nil, 0, false
	}
	for _, id := range []string{"auditlog", "lastbind", "memberof", "ppolicy", "refint", "syncprov", "unique"} {
		o := st.Overlays[id]
		if !overlays.Active(id, o) {
			continue
		}
		isCarried := false
		for _, c := range carriedOverlays {
			if c == id {
				isCarried = true
			}
		}
		if isCarried {
			carry = append(carry, migOverlay{ID: id, Values: overlays.Values(id, o, nil)})
		} else {
			skip = append(skip, id)
		}
	}
	if e, err := cc.GetAttrs(st.DBDN, []string{"olcAccess"}); err == nil {
		aclRules = len(e.Values("olcAccess"))
	}
	return carry, skip, aclRules, true
}

var overlayNames = map[string]string{
	"memberof": "memberOf", "refint": "referential integrity", "unique": "unique values", "lastbind": "last sign-in",
	"ppolicy": "password rules", "syncprov": "replication", "auditlog": "change logging",
}

func overlayLabel(id string) string {
	if n, ok := overlayNames[id]; ok {
		return n
	}
	return id
}

// analyse reads the old directory and compares it with what the new server can hold.
func (s *Server) analyse(sess *session.Session, creds migCreds) (*migCheck, []ldapclient.Entry, error) {
	src, err := s.dialSource(creds)
	if err != nil {
		return nil, nil, err
	}
	defer src.Close()
	suffix := s.sourceSuffix(src)
	entries, err := src.SearchComplete(suffix, "(objectClass=*)", []string{"*"})
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't read the old directory: %w", err)
	}
	if len(entries) > maxMigrationEntries {
		return nil, nil, validationError(fmt.Sprintf("The old directory has %d entries, more than this tool carries over in one go (%d).", len(entries), maxMigrationEntries))
	}
	rep := &migCheck{Suffix: suffix, Entries: len(entries), Kinds: map[string]int{}, Unsupported: []migUnsupported{}, Hashes: []migHash{},
		Carry: []string{}, NotCarried: []string{}, Blockers: []string{}, Warnings: []string{}}
	for _, e := range entries {
		rep.Kinds[migKind(e)]++
	}
	if len(entries) == 0 {
		rep.Blockers = append(rep.Blockers, "Nothing was found under "+suffix+" on the old server. Check the directory manager's name and that it can read the whole directory.")
	}

	// password hashes
	schemes := map[string]int{}
	for _, e := range entries {
		for _, v := range e.Values("userPassword") {
			if sc := hashScheme(v); sc == "" {
				rep.PlainText++
			} else {
				schemes[sc]++
			}
		}
	}
	names := make([]string, 0, len(schemes))
	for k := range schemes {
		names = append(names, k)
	}
	sort.Strings(names)
	unsupportedHashes := 0
	for _, k := range names {
		ok := supportedHashes[k]
		rep.Hashes = append(rep.Hashes, migHash{Scheme: k, Count: schemes[k], Supported: ok})
		if !ok {
			unsupportedHashes += schemes[k]
		}
	}
	if unsupportedHashes > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d passwords use a storage method the new server can't check. Those people won't be able to sign in until their password is reset.", unsupportedHashes))
	}
	if rep.PlainText > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d passwords are stored in plain text on the old server. They are copied as they are; reset them afterwards so they are stored safely.", rep.PlainText))
	}

	// the new server
	h, herr := readHandoff()
	if herr != nil {
		_, why := diagnoseNoHandoff(herr)
		rep.Blockers = append(rep.Blockers, why)
	} else {
		rep.Host = h.Host
		cc, err := s.configConn(h)
		if err != nil {
			rep.Blockers = append(rep.Blockers, "Dapper found the new server's first-start file but couldn't connect to it: "+err.Error()+".")
		} else {
			defer cc.Close()
			rep.TargetFound = true
			classes, e1 := cc.ObjectClassNames()
			attrs, e2 := cc.AttributeNames()
			if e1 != nil || e2 != nil || classes == nil || attrs == nil {
				rep.Warnings = append(rep.Warnings, "Dapper couldn't read the new server's schema, so it can't tell you in advance whether everything will fit.")
			} else {
				type acc struct {
					kind, name string
					n          int
					sample     []string
				}
				found := map[string]*acc{}
				affected := 0
				for _, e := range entries {
					cls, att := unsupportedFor(e, classes, attrs)
					if len(cls)+len(att) == 0 {
						continue
					}
					affected++
					add := func(kind, name string) {
						k := kind + "|" + strings.ToLower(name)
						a := found[k]
						if a == nil {
							a = &acc{kind: kind, name: name}
							found[k] = a
						}
						a.n++
						if len(a.sample) < 3 {
							a.sample = append(a.sample, e.DN)
						}
					}
					for _, c := range cls {
						add("object class", c)
					}
					for _, a := range att {
						add("attribute", a)
					}
				}
				for _, a := range found {
					rep.Unsupported = append(rep.Unsupported, migUnsupported{Kind: a.kind, Name: a.name, Count: a.n, Sample: a.sample})
				}
				sort.Slice(rep.Unsupported, func(i, j int) bool {
					if rep.Unsupported[i].Count != rep.Unsupported[j].Count {
						return rep.Unsupported[i].Count > rep.Unsupported[j].Count
					}
					return rep.Unsupported[i].Name < rep.Unsupported[j].Name
				})
				rep.Affected = affected
				if affected > 0 {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d entries use schema the new server doesn't have. Add the schema to the new server first (Server → Schemas), or choose to leave those entries out.", affected))
				}
			}
		}
	}

	// features on the old server
	carry, skip, acls, known := s.sourceOverlays(sess)
	rep.OverlaysKnown = known
	for _, c := range carry {
		rep.Carry = append(rep.Carry, c.ID)
	}
	if known {
		for _, id := range skip {
			rep.NotCarried = append(rep.NotCarried, "The “"+overlayLabel(id)+"” feature is on at the old server. Set it up again on the new one under Server after switching.")
		}
		if acls > 0 {
			rep.NotCarried = append(rep.NotCarried, fmt.Sprintf("The old server's %d access rules aren't copied. The new server starts with sensible defaults; review them under Server.", acls))
		}
	} else {
		rep.Warnings = append(rep.Warnings, "Unlock the server settings on the Server page first if you want Dapper to carry over features such as memberOf and to list what it can't carry over.")
	}
	rep.NotCarried = append(rep.NotCarried,
		"Indexes, the server's certificate, replication settings and anything else in the old server's own configuration aren't copied. The new server has its own certificate and indexes for the usual attributes.",
		"When each person last changed their password isn't copied, so password expiry clocks start again from the move.")
	return rep, entries, nil
}

func (s *Server) migrationEligible(w http.ResponseWriter) bool {
	if s.dapperRuns() {
		fail(w, http.StatusConflict, "This directory server is already run by Dapper, so there is nothing to migrate.")
		return false
	}
	return true
}

func (s *Server) migrationCheckHandler(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if !s.migrationEligible(w) {
		return
	}
	var in migCreds
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.SourceDN) == "" || in.SourcePassword == "" {
		fail(w, http.StatusBadRequest, "Enter the old server's directory manager name and password.")
		return
	}
	rep, _, err := s.analyse(sess, in)
	if err != nil {
		failLDAP(w, err)
		return
	}
	audit(sess, "migration.check", rep.Suffix)
	writeJSON(w, rep)
}

// diagnoseNoHandoff says, in plain words, why Dapper has no first-start file from a new server to work with.
func diagnoseNoHandoff(readErr error) (reason, message string) {
	if !os.IsNotExist(readErr) {
		return "unusable", "Dapper found the new server's first-start file but couldn't use it: " + readErr.Error() + "."
	}
	dir := filepath.Dir(handoffPath())
	if _, err := os.Stat(dir); err != nil {
		return "no-volume", "Dapper wasn't started together with its directory server: the folder they share (" + dir + ") isn't there. Start both with the command below."
	}
	u, err := url.Parse(bundledURL())
	if err != nil || u.Host == "" {
		return "server-down", "Dapper can't tell where the new server is. Check DAPPER_BUNDLED_URL."
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "636")
	}
	c, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return "server-down", "Dapper can't reach the new server at " + host + ". It isn't running, or it isn't on the same Docker network as Dapper. Start it with the command below."
	}
	c.Close()
	return "already-used", "The new server is running, but its one-time first-start file is gone, which means it was already set up (or a migration already used it). A new server can be migrated into only once, while it is still empty. To start over with a fresh one, see “If something goes wrong” in docs/migrating.md."
}

// ---- status ----

func (s *Server) migrationStatus(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	cfg := s.c()
	out := map[string]any{"dapperRun": s.dapperRuns(), "job": s.migJobSnapshot()}
	if cfg.PreviousLDAP != nil {
		out["canRollback"] = true
		out["previousURL"] = cfg.PreviousLDAP.URL
	}
	if !s.dapperRuns() {
		out["sourceURL"] = cfg.LDAP.URL
		nf := map[string]any{"found": false}
		h, err := readHandoff()
		if err == nil {
			nf["found"], nf["host"] = true, h.Host
			if cc, err := s.configConn(h); err == nil {
				cc.Close()
				nf["reachable"] = true
			} else {
				nf["reachable"], nf["message"] = false, "Dapper found the new server's first-start file but couldn't connect to it: "+err.Error()+"."
			}
		} else {
			nf["reason"], nf["message"] = diagnoseNoHandoff(err)
		}
		out["newServer"] = nf
	}
	s.mig.mu.Lock()
	out["readyToSwitch"] = s.mig.target.verified && s.mig.target.managerPW != ""
	out["suffix"] = s.mig.target.suffix
	s.mig.mu.Unlock()
	writeJSON(w, out)
}

// ---- the copy ----

func (s *Server) migrationCopy(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if !s.migrationEligible(w) {
		return
	}
	var in migCreds
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.SourceDN) == "" || in.SourcePassword == "" {
		fail(w, http.StatusBadRequest, "Enter the old server's directory manager name and password.")
		return
	}
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the copy first.")
		return
	}
	s.mig.mu.Lock()
	if s.mig.job.Running {
		s.mig.mu.Unlock()
		fail(w, http.StatusConflict, "A copy is already running.")
		return
	}
	s.mig.mu.Unlock()

	// check first, on the same terms the person saw
	rep, _, err := s.analyse(sess, in)
	if err != nil {
		failLDAP(w, err)
		return
	}
	if len(rep.Blockers) > 0 {
		fail(w, http.StatusBadRequest, rep.Blockers[0])
		return
	}
	if rep.Affected > 0 && !in.SkipUnsupported {
		fail(w, http.StatusBadRequest, "Some entries use schema the new server doesn't have. Add it to the new server, or choose to leave those entries out.")
		return
	}
	carry, _, _, _ := s.sourceOverlays(sess)
	ctx := s.overlayContext()
	placeholder := s.placeholderDN()

	s.mig.mu.Lock()
	s.mig.job = migJob{Running: true, Phase: "Starting", Started: time.Now()}
	s.mig.target = migTarget{}
	s.mig.mu.Unlock()
	audit(sess, "migration.copy", rep.Suffix)

	go s.runMigration(in, rep.Suffix, carry, ctx, placeholder)
	writeJSONStatus(w, http.StatusAccepted, map[string]bool{"started": true})
}

func (s *Server) migSet(f func(j *migJob)) {
	s.mig.mu.Lock()
	f(&s.mig.job)
	s.mig.mu.Unlock()
}

func (s *Server) migFail(msg string) {
	s.migSet(func(j *migJob) { j.Running, j.Error, j.Finished = false, msg, time.Now() })
	log.Printf("migration failed: %s", msg)
}

func (s *Server) runMigration(creds migCreds, suffix string, carry []migOverlay, ctx overlays.Context, placeholder string) {
	ctx.PlaceholderDN = placeholder
	s.migSet(func(j *migJob) { j.Phase = "Connecting to the new server" })
	h, err := readHandoff()
	if err != nil {
		s.migFail("Dapper can't find the new server's first-start file any more.")
		return
	}
	cc, err := s.configConn(h)
	if err != nil {
		s.migFail("Couldn't connect to the new server: " + err.Error())
		return
	}
	defer cc.Close()

	managerDN := "cn=admin," + suffix
	managerPW := generatePassword(24)
	s.migSet(func(j *migJob) { j.Phase = "Creating the directory on the new server" })
	dbDN, err := createDatabase(cc, suffix, managerDN, managerPW)
	if err != nil {
		_, msg := friendlyOverlayError(err, "the directory")
		s.migFail("Couldn't create the directory on the new server. " + msg)
		return
	}
	var carried []string
	st := &overlays.State{DBDN: dbDN, Suffix: suffix, ModuleDN: "cn=module{0},cn=config", Overlays: map[string]*overlays.Overlay{}}
	st.Modules = []string{"memberof", "refint", "unique", "lastbind", "auditlog", "ppolicy", "syncprov"}
	for _, o := range carry {
		plan, err := overlays.BuildPlan(ctx, st, o.ID, "enable", o.Values, overlays.Facts{})
		if err != nil {
			s.migFail("Couldn't turn on “" + overlayLabel(o.ID) + "” on the new server: " + err.Error())
			return
		}
		for _, op := range plan.Ops {
			if op.Target == "data" {
				continue
			}
			if err := applyOp(cc, op); err != nil && !ldapclient.IsCode(err, ldapclient.CodeAlreadyExists) {
				s.migFail("Couldn't turn on “" + overlayLabel(o.ID) + "” on the new server: " + err.Error())
				return
			}
		}
		carried = append(carried, o.ID)
	}

	tgt, err := ldapclient.Dial(ldapclient.Server{URL: bundledURL(), CAFile: s.caPath()})
	if err != nil {
		s.migFail("Couldn't connect to the new directory: " + err.Error())
		return
	}
	defer tgt.Close()
	if err := tgt.Bind(managerDN, managerPW); err != nil {
		s.migFail("Couldn't sign in to the new directory as its manager: " + err.Error())
		return
	}
	tgt.KeepSecrets() // so the check can compare password hashes too

	s.migSet(func(j *migJob) { j.Phase = "Reading the old directory" })
	src, err := s.dialSource(creds)
	if err != nil {
		s.migFail(err.Error())
		return
	}
	defer src.Close()
	entries, err := src.SearchComplete(suffix, "(objectClass=*)", []string{"*"})
	if err != nil {
		s.migFail("Couldn't read the old directory: " + err.Error())
		return
	}
	classes, _ := cc.ObjectClassNames()
	attrs, _ := cc.AttributeNames()

	res := &migResult{SourceEntries: len(entries), CarriedOverlays: carried}
	var todo []ldapclient.Entry
	skipped := map[string]bool{}
	for _, e := range entries {
		if classes != nil && attrs != nil {
			if cls, att := unsupportedFor(e, classes, attrs); len(cls)+len(att) > 0 {
				res.Skipped++
				skipped[strings.ToLower(e.DN)] = true
				continue
			}
		}
		todo = append(todo, e)
	}
	todo = orderForCopy(todo)
	s.migSet(func(j *migJob) { j.Phase, j.Total, j.Done = "Copying", 2*len(todo), 0 })

	fail := func(dn string, err error) {
		res.Failed++
		if len(res.Failures) < 20 {
			res.Failures = append(res.Failures, dn+": "+err.Error())
		}
	}
	for i, e := range todo {
		err := tgt.Add(e)
		switch {
		case err == nil:
			res.Added++
		case ldapclient.IsCode(err, ldapclient.CodeAlreadyExists):
			cur, gerr := tgt.GetAttrs(e.DN, []string{"*"})
			if gerr != nil {
				fail(e.DN, gerr)
				break
			}
			if ch := diffAttrs(e.Attrs, cur.Attrs); len(ch) == 0 {
				res.Unchanged++
			} else if merr := tgt.Modify(e.DN, ch); merr != nil {
				fail(e.DN, merr)
			} else {
				res.Updated++
			}
		default:
			fail(e.DN, err)
		}
		if i%25 == 0 {
			s.migSet(func(j *migJob) { j.Done = i })
		}
	}

	// check the copy: every entry on the new server must equal the old one
	s.migSet(func(j *migJob) { j.Phase, j.Done = "Checking the copy", len(todo) })
	back, err := tgt.SearchComplete(suffix, "(objectClass=*)", []string{"*"})
	if err != nil {
		s.migFail("The copy was made, but Dapper couldn't read the new directory to check it: " + err.Error())
		return
	}
	got := map[string]ldapclient.Entry{}
	for _, e := range back {
		got[strings.ToLower(e.DN)] = e
	}
	want := map[string]bool{}
	for i, e := range todo {
		want[strings.ToLower(e.DN)] = true
		cur, ok := got[strings.ToLower(e.DN)]
		if ok && len(diffAttrs(e.Attrs, cur.Attrs)) == 0 {
			res.Identical++
		} else {
			res.Mismatched++
			if len(res.MismatchSample) < 10 {
				res.MismatchSample = append(res.MismatchSample, e.DN)
			}
		}
		if i%50 == 0 {
			s.migSet(func(j *migJob) { j.Done = len(todo) + i })
		}
	}
	for _, e := range back {
		if !want[strings.ToLower(e.DN)] && !skipped[strings.ToLower(e.DN)] {
			res.Extras++
			if len(res.ExtrasSample) < 10 {
				res.ExtrasSample = append(res.ExtrasSample, e.DN)
			}
		}
	}
	res.Verified = res.Failed == 0 && res.Mismatched == 0

	s.mig.mu.Lock()
	s.mig.target = migTarget{suffix: suffix, managerPW: managerPW, verified: res.Verified}
	s.mig.job.Running, s.mig.job.Phase, s.mig.job.Done, s.mig.job.Total = false, "Finished", len(todo)*2, len(todo)*2
	s.mig.job.Result, s.mig.job.Finished = res, time.Now()
	s.mig.mu.Unlock()
	log.Printf("migration copy finished: %d added, %d updated, %d unchanged, %d failed, verified=%v", res.Added, res.Updated, res.Unchanged, res.Failed, res.Verified)
}

// ---- switching over, and back ----

func cutoverChecklist(host, suffix string, r *migResult) []string {
	l := []string{
		"Stop making changes on the old server while you switch. If anything changed since the last copy, run the copy again first: it updates what changed.",
		"Point your DNS name (or the address your machines use for the directory) at the new server. Dapper can't change DNS for you.",
		"Make sure ports 636 (LDAPS) and/or 389 (StartTLS) are open to your machines. The new server only accepts encrypted connections.",
		"Give every machine and application the new server's certificate to trust (Server → Connect your machines has the download and commands).",
		"Update `/etc/sssd/sssd.conf` or `ldap.conf` where the address, certificate or search base changed. The directory name stays " + suffix + ".",
		"Review the new server's access rules under Server (they are not copied from the old one).",
		"Turn on again, under Server, any feature that was on at the old server and isn't listed as carried over (password rules, replication, change logging).",
		"People who use SSH keys, sudo or other attributes keep them: they were copied with their entries.",
		"Password expiry clocks start again from the move, because when each password was last changed isn't copied.",
		"Keep the old server running until everything works. Rollback in Dapper is one click until you finish the migration.",
	}
	if host == "" || host == "localhost" {
		l = append([]string{"The new server's certificate is for “localhost”. Set LDAP_HOSTNAME to your real name and recreate the server's certificate before switching real machines."}, l...)
	}
	if r != nil && r.Skipped > 0 {
		l = append(l, fmt.Sprintf("%d entries were left out because they use schema the new server doesn't have.", r.Skipped))
	}
	return l
}

func (s *Server) migrationSwitch(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if !s.migrationEligible(w) {
		return
	}
	var in struct{ Confirmed bool }
	_ = json.NewDecoder(r.Body).Decode(&in)
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the switch first.")
		return
	}
	s.mig.mu.Lock()
	t := s.mig.target
	res := s.mig.job.Result
	running := s.mig.job.Running
	s.mig.mu.Unlock()
	if running {
		fail(w, http.StatusConflict, "A copy is still running.")
		return
	}
	if !t.verified || t.managerPW == "" {
		fail(w, http.StatusConflict, "Run the copy first and wait for it to be checked. (If Dapper was restarted since the copy, run it again; that is quick and safe.)")
		return
	}
	h, err := readHandoff()
	if err != nil {
		fail(w, http.StatusConflict, "Dapper can't find the new server's first-start file any more.")
		return
	}

	cfg := *s.c()
	prev := cfg.LDAP
	cfg.PreviousLDAP = &prev
	cfg.LDAP = config.LDAP{URL: bundledURL(), BindDN: "cn=admin," + t.suffix, BindPassword: t.managerPW, CAFile: s.caPath(), PublicHost: h.Host}
	checks, cc := s.runChecks(&cfg)
	if cc != nil {
		cc.Close()
	}
	if !allOK(checks) {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "The new directory isn't ready to be used by Dapper yet, so nothing was switched.", "checks": checks})
		return
	}
	if err := s.store.Save(&cfg); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error()+".")
		return
	}
	s.sessions.RevokeAll("")
	_ = os.Remove(handoffPath())
	s.mig.mu.Lock()
	s.mig.target = migTarget{}
	s.mig.mu.Unlock()
	audit(sess, "migration.switch", t.suffix)
	writeJSON(w, map[string]any{
		"ok": true, "suffix": t.suffix, "host": h.Host,
		"managerDN": "cn=admin," + t.suffix, "managerPassword": t.managerPW,
		"configDN": h.ConfigDN, "configPassword": h.ConfigPassword, "certificate": h.Certificate,
		"checklist": cutoverChecklist(h.Host, t.suffix, res),
	})
}

// migrationRollback puts Dapper back on the old server.
func (s *Server) migrationRollback(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ Confirmed bool }
	_ = json.NewDecoder(r.Body).Decode(&in)
	cfg := *s.c()
	if cfg.PreviousLDAP == nil {
		fail(w, http.StatusConflict, "There is nothing to roll back to.")
		return
	}
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the rollback first.")
		return
	}
	back := cfg
	back.LDAP = *cfg.PreviousLDAP
	back.PreviousLDAP = nil
	c, err := dialSvc(&back)
	if err != nil {
		fail(w, http.StatusBadGateway, "Dapper can't reach the old server with its saved connection, so it stays on the new one: "+err.Error()+".")
		return
	}
	c.Close()
	if err := s.store.Save(&back); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error()+".")
		return
	}
	s.sessions.RevokeAll("")
	audit(sess, "migration.rollback", back.LDAP.URL)
	writeJSON(w, map[string]any{"ok": true, "url": back.LDAP.URL})
}

// migrationFinish forgets the old connection (and its saved password) once the person is happy.
func (s *Server) migrationFinish(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ Confirmed bool }
	_ = json.NewDecoder(r.Body).Decode(&in)
	cfg := *s.c()
	if cfg.PreviousLDAP == nil {
		fail(w, http.StatusConflict, "The migration is already finished.")
		return
	}
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve finishing first: after this Dapper forgets the old server's saved connection and you can't roll back.")
		return
	}
	cfg.PreviousLDAP = nil
	if err := s.store.Save(&cfg); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error()+".")
		return
	}
	audit(sess, "migration.finish", "")
	writeJSON(w, map[string]bool{"ok": true})
}
