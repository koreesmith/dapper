package api

import (
	"errors"
	"net/http"
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
