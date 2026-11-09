package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
)

func TestUnderBase(t *testing.T) {
	base := "ou=people,dc=example,dc=org"
	cases := map[string]bool{
		"uid=a,ou=people,dc=example,dc=org":      true,
		"UID=A,OU=People,DC=Example,DC=Org":      true, // case-insensitive
		"uid=a,ou=x,ou=people,dc=example,dc=org": true,
		"ou=people,dc=example,dc=org":            false, // the container itself
		"cn=admin,dc=example,dc=org":             false,
		"uid=a,ou=people,dc=evil,dc=org":         false,
		"uid=a\\,ou=people,dc=example,dc=org":    false, // escaped comma is part of the value
		"uid=a,ou=notpeople,dc=example,dc=org":   false,
		"garbage":                                false,
		"":                                       false,
	}
	for dn, want := range cases {
		if got := underBase(base, dn); got != want {
			t.Errorf("underBase(%q) = %v, want %v", dn, got, want)
		}
	}
	if underBase("", "uid=a,ou=people,dc=example,dc=org") {
		t.Error("an unset base must never match")
	}
}

func TestRDNHelpers(t *testing.T) {
	if got := rdnOf(`uid=a\,b,ou=people,dc=example,dc=org`); got != `uid=a\,b` {
		t.Errorf("rdnOf = %q", got)
	}
	if got := escapeRDN(`a,b+c="d"`); got != `a\,b\+c\=\"d\"` {
		t.Errorf("escapeRDN = %q", got)
	}
	if got := escapeRDN("#x"); got != `\#x` {
		t.Errorf("leading # must be escaped: %q", got)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter()
	for i := 0; i < maxFailures-1; i++ {
		l.fail("user:a")
	}
	if l.blocked("user:a") {
		t.Fatal("blocked too early")
	}
	l.fail("user:a")
	if !l.blocked("user:a") {
		t.Fatal("should block at the limit")
	}
	if l.blocked("user:b") {
		t.Fatal("limits are per key")
	}
	l.reset("user:a")
	if l.blocked("user:a") {
		t.Fatal("reset should clear")
	}
	for i := 0; i < maxFailures; i++ {
		l.fail("ip:1.2.3.4")
	}
	if l.blocked("ip:1.2.3.4") {
		t.Fatal("IP limit must be looser than the per-account limit")
	}
}

func TestFriendlyError(t *testing.T) {
	cases := map[uint16]int{
		ldap.LDAPResultInsufficientAccessRights: 403,
		ldap.LDAPResultEntryAlreadyExists:       409,
		ldap.LDAPResultNoSuchObject:             404,
		ldap.LDAPResultConstraintViolation:      400,
	}
	for code, status := range cases {
		got, msg := friendlyError(ldap.NewError(code, errors.New("detail")))
		if got != status || msg == "" {
			t.Errorf("code %d -> %d %q", code, got, msg)
		}
	}
	if got, _ := friendlyError(errors.New("boom")); got != 500 {
		t.Error("unknown errors are 500")
	}
}

func TestGeneratePassword(t *testing.T) {
	a, b := generatePassword(14), generatePassword(14)
	if len(a) != 14 || a == b {
		t.Errorf("bad generated passwords: %q %q", a, b)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	pol, err := policy.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Password.MinLength = 8
	return &Server{store: config.Static(cfg), policy: pol}
}

func entryOf(attrs map[string][]string) *ldapclient.Entry {
	return &ldapclient.Entry{DN: "uid=x", Attrs: attrs}
}

func TestBuildChangesEnforcesPolicy(t *testing.T) {
	s := newTestServer(t)
	cur := entryOf(map[string][]string{"givenName": {"Ann"}, "sn": {"Lee"}, "cn": {"Ann Lee"}, "displayName": {"Ann Lee"}, "mail": {"a@x.org"}})
	userEditable := func(f policy.Field) bool { return f.UserEditable }

	// Allowed field changes.
	ch, err := s.buildChanges(cur, map[string][]string{"mail": {"new@x.org"}}, userEditable)
	if err != nil || len(ch) != 1 || ch[0].Attr != "mail" {
		t.Fatalf("mail change: %v %v", ch, err)
	}
	// Unchanged values produce no modification.
	if ch, _ := s.buildChanges(cur, map[string][]string{"mail": {"a@x.org"}}, userEditable); len(ch) != 0 {
		t.Errorf("unchanged value produced %v", ch)
	}
	// Locked, admin-only and unknown attributes are refused.
	var fe forbiddenError
	if _, err := s.buildChanges(cur, map[string][]string{"givenName": {"Eve"}}, userEditable); !errors.As(err, &fe) {
		t.Errorf("locked field should be forbidden, got %v", err)
	}
	if _, err := s.buildChanges(cur, map[string][]string{"description": {"x"}}, userEditable); !errors.As(err, &fe) {
		t.Errorf("admin-only field should be forbidden, got %v", err)
	}
	var ve validationError
	if _, err := s.buildChanges(cur, map[string][]string{"userPassword": {"x"}}, userEditable); !errors.As(err, &ve) {
		t.Errorf("unknown attribute should be a validation error, got %v", err)
	}
	if _, err := s.buildChanges(cur, map[string][]string{"mail": {"not-an-email"}}, userEditable); !errors.As(err, &ve) {
		t.Errorf("invalid email should be rejected, got %v", err)
	}
	// Admins may rename; cn and the derived display name follow.
	all := func(policy.Field) bool { return true }
	ch, err = s.buildChanges(cur, map[string][]string{"givenName": {"Anna"}}, all)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range ch {
		got[c.Attr] = c.Values[0]
	}
	if got["cn"] != "Anna Lee" || got["displayName"] != "Anna Lee" {
		t.Errorf("name change should update cn and derived displayName: %v", got)
	}
	// A deliberately different display name is left alone.
	cur.Attrs["displayName"] = []string{"Annie"}
	ch, _ = s.buildChanges(cur, map[string][]string{"givenName": {"Anna"}}, all)
	for _, c := range ch {
		if c.Attr == "displayName" {
			t.Error("custom display name must not be overwritten")
		}
	}
	// Required fields can't be blanked.
	if _, err := s.buildChanges(cur, map[string][]string{"sn": {""}}, all); !errors.As(err, &ve) {
		t.Errorf("blank required field should be rejected, got %v", err)
	}
}

func TestCheckPassword(t *testing.T) {
	s := newTestServer(t)
	if s.checkPassword("short") == nil || s.checkPassword("long-enough-1") != nil {
		t.Error("minimum length not enforced")
	}
}

func TestClientIP(t *testing.T) {
	req := func(remote, xff string) *http.Request {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"203.0.113.9:5000", "198.51.100.1", "203.0.113.9"},          // public peer: header is untrusted
		{"127.0.0.1:5000", "198.51.100.7", "198.51.100.7"},           // proxy on this host
		{"172.18.0.2:5000", "198.51.100.7", "198.51.100.7"},          // proxy on a private network
		{"172.18.0.2:5000", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // spoofed prefix ignored
		{"172.18.0.2:5000", "not-an-ip", "172.18.0.2"},
		{"172.18.0.2:5000", "", "172.18.0.2"},
	}
	for _, c := range cases {
		if got := clientIP(req(c.remote, c.xff)); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestMissingClasses(t *testing.T) {
	have := map[string]bool{"posixaccount": true, "posixgroup": true}
	if m := missingClasses(have, []string{"posixAccount", "posixGroup"}); len(m) != 0 {
		t.Errorf("nothing should be missing: %v", m)
	}
	m := missingClasses(have, []string{"posixAccount", "shadowAccount", "ldapPublicKey"})
	if len(m) != 2 || m[0] != "shadowAccount" || m[1] != "ldapPublicKey" {
		t.Errorf("got %v", m)
	}
}

func TestAddUniqueAndSuffix(t *testing.T) {
	got := addUnique([]string{"inetOrgPerson", "POSIXACCOUNT"}, "posixAccount", "shadowAccount")
	if len(got) != 3 || got[2] != "shadowAccount" {
		t.Errorf("addUnique: %v", got)
	}
	if suffixOf("ou=people,dc=example,dc=org") != "dc=example,dc=org" || suffixOf("ou=people,o=corp") != "ou=people,o=corp" {
		t.Error("suffixOf")
	}
	if rdnOf2 := privateGroupDN(&config.Config{Settings: config.Settings{Groups: config.Groups{BaseDN: "ou=g,dc=x"}}}, "a,b"); rdnOf2 != `cn=a\,b,ou=g,dc=x` {
		t.Errorf("privateGroupDN escaping: %s", rdnOf2)
	}
}

func TestSuggestContainers(t *testing.T) {
	suffix := "dc=kasasmith,dc=com"
	got := suggestContainers(suffix, []string{"ou=group,dc=kasasmith,dc=com", "ou=People,dc=kasasmith,dc=com", "ou=Disabled,dc=kasasmith,dc=com", "ou=hosts,dc=kasasmith,dc=com"})
	want := map[string]string{
		"usersBaseDN":     "ou=People,dc=kasasmith,dc=com",
		"groupsBaseDN":    "ou=group,dc=kasasmith,dc=com",
		"suspendedBaseDN": "ou=Disabled,dc=kasasmith,dc=com",
		"adminGroupDN":    "cn=dapper-admins,ou=group,dc=kasasmith,dc=com",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// "groups" beats "group" when both exist; nothing found falls back to conventional names.
	if g := suggestContainers(suffix, []string{"ou=group,dc=x", "ou=groups,dc=x"})["groupsBaseDN"]; g != "ou=groups,dc=x" {
		t.Errorf("preference order: %s", g)
	}
	empty := suggestContainers(suffix, nil)
	if empty["usersBaseDN"] != "ou=people,"+suffix || empty["groupsBaseDN"] != "ou=groups,"+suffix || empty["suspendedBaseDN"] != "ou=suspended,"+suffix {
		t.Errorf("fallbacks: %v", empty)
	}
}

func TestRenameKeepsTheNamingValue(t *testing.T) {
	s := newTestServer(t)
	// An account named cn=koree: the value "koree" must survive a name change.
	cur := &ldapclient.Entry{DN: "cn=koree,ou=People,dc=kasasmith,dc=com", Attrs: map[string][]string{
		"cn": {"koree", "Koree Smith"}, "givenName": {"Koree"}, "sn": {"Smith"}, "displayName": {"Koree Smith"}, "objectClass": {"inetOrgPerson"},
	}}
	all := func(policy.Field) bool { return true }
	ch, err := s.buildChanges(cur, map[string][]string{"givenName": {"Kor"}, "sn": {"Smyth"}}, all)
	if err != nil {
		t.Fatal(err)
	}
	var cn []string
	for _, c := range ch {
		if c.Attr == "cn" {
			cn = c.Values
		}
	}
	has := func(vs []string, w string) bool {
		for _, v := range vs {
			if v == w {
				return true
			}
		}
		return false
	}
	if !has(cn, "koree") || !has(cn, "Kor Smyth") || has(cn, "Koree Smith") {
		t.Errorf("cn after rename = %v", cn)
	}

	// The same when the account's cn holds only the full name and it is named by uid: it is replaced.
	cur2 := &ldapclient.Entry{DN: "uid=jo,ou=people,dc=x", Attrs: map[string][]string{
		"cn": {"Jo Lee"}, "givenName": {"Jo"}, "sn": {"Lee"}, "displayName": {"Jo Lee"}, "uid": {"jo"},
	}}
	ch, _ = s.buildChanges(cur2, map[string][]string{"sn": {"Li"}}, all)
	for _, c := range ch {
		if c.Attr == "cn" && (len(c.Values) != 1 || c.Values[0] != "Jo Li") {
			t.Errorf("uid-named cn = %v", c.Values)
		}
	}
	// If the new name equals one already present, it isn't duplicated.
	if got := syncFullName(cur, "Koree Smith", "koree"); len(got) != 1 || got[0] != "koree" {
		t.Errorf("no duplicates: %v", got)
	}
}

func TestNewAccountNamedByCN(t *testing.T) {
	s := newTestServer(t)
	cfg := s.c()
	cfg.Users.RDNAttribute = "cn"
	attrs, err := s.personAttrs(cfg, "casey", map[string][]string{"givenName": {"Casey"}, "sn": {"Jones"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attrs["cn"]) != 2 || attrs["cn"][0] != "casey" || attrs["cn"][1] != "Casey Jones" {
		t.Errorf("cn must include the naming value: %v", attrs["cn"])
	}
	cfg.Users.RDNAttribute = "uid"
	attrs, _ = s.personAttrs(cfg, "casey", map[string][]string{"givenName": {"Casey"}, "sn": {"Jones"}})
	if len(attrs["cn"]) != 1 || attrs["cn"][0] != "Casey Jones" {
		t.Errorf("uid-named accounts keep a single cn: %v", attrs["cn"])
	}
}

func TestPasswordRuleMessages(t *testing.T) {
	for diag, want := range map[string]string{
		"Password is in history of old passwords": "used that password before",
		"Password fails quality checking policy":  "doesn't meet your organization's password rules",
		"Password is too short for policy":        "too short",
		"Password is too young to change":         "too recently",
	} {
		code, msg := friendlyError(ldap.NewError(ldap.LDAPResultConstraintViolation, errors.New(diag)))
		if code != 400 || !strings.Contains(msg, want) {
			t.Errorf("%q -> %d %q, want it to mention %q", diag, code, msg, want)
		}
	}
	// A different constraint violation keeps the generic wording.
	if _, msg := friendlyError(ldap.NewError(ldap.LDAPResultConstraintViolation, errors.New("something else"))); strings.Contains(msg, "password rules") {
		t.Errorf("unrelated violations shouldn't claim to be about passwords: %q", msg)
	}
}

func TestParseImport(t *testing.T) {
	s := newTestServer(t)
	good := "\xef\xbb\xbfFirst Name,Surname,E-mail,Cell,Dept\r\n" +
		"Ada,Lovelace,ada@example.org,+1 555 0100,Math\r\n" +
		"\"O'Brien, Jr\",Pat,pat@example.org,,\r\n" +
		"Zoë,Müller,,,\r\n"
	people, rows, err := s.parseImport(good, []string{"cn=g,ou=groups,dc=x"})
	if err != nil || len(people) != 3 || len(rows) != 3 {
		t.Fatalf("parse: %v %d %d", err, len(people), len(rows))
	}
	if people[0].Username != "ada.lovelace" || people[0].Attrs["mail"][0] != "ada@example.org" || people[0].Attrs["mobile"][0] != "+1 555 0100" || people[0].Attrs["departmentNumber"][0] != "Math" {
		t.Errorf("row 1: %+v", people[0])
	}
	if people[1].Attrs["givenName"][0] != "O'Brien, Jr" || people[1].Username != "obrienjr.pat" {
		t.Errorf("quoted comma and punctuation in names: %+v", people[1])
	}
	if people[2].Username != "zo.mller" {
		t.Errorf("non-ASCII letters are dropped from the suggested username: %q", people[2].Username)
	}
	if rows[0].Row != 2 || rows[2].Row != 4 || len(people[0].Groups) != 1 {
		t.Errorf("row numbers follow the file (header is row 1); groups are passed on: %+v", rows)
	}
	// An explicit username wins, and is lower-cased.
	p2, _, _ := s.parseImport("username,first name,last name\nSomeone,A,B\n", nil)
	if p2[0].Username != "someone" {
		t.Errorf("explicit username: %q", p2[0].Username)
	}
	for name, bad := range map[string]string{
		"empty":         "",
		"header only":   "first name,last name\n",
		"no first name": "last name,email\nX,y@z.org\n",
		"no last name":  "first name,email\nX,y@z.org\n",
		"not a csv":     "a,b\n\"unterminated\n",
	} {
		if _, _, err := s.parseImport(bad, nil); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	var sb strings.Builder
	sb.WriteString("first name,last name\n")
	for i := 0; i < maxImportRows+1; i++ {
		sb.WriteString("A,B\n")
	}
	if _, _, err := s.parseImport(sb.String(), nil); err == nil {
		t.Error("more than the row limit must be refused")
	}
}

func TestSuggestUsernameAndCSVSafe(t *testing.T) {
	for in, want := range map[[2]string]string{{"Ada", "Lovelace"}: "ada.lovelace", {"Jo-Ann", "O'Neil"}: "joann.oneil", {"", "Solo"}: "solo", {"X", ""}: "x"} {
		if got := suggestUsername(in[0], in[1]); got != want {
			t.Errorf("suggestUsername(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"=1+1": "'=1+1", "+x": "'+x", "-x": "'-x", "@x": "'@x", "plain": "plain", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
