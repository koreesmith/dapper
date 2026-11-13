package api

import (
	"crypto/sha1"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestServiceNames(t *testing.T) {
	for n, want := range map[string]bool{"sssd": true, "nextcloud": true, "zabbix-ro": true, "app_1": true, "a": false, "1abc": false, "Upper": false, "has space": false, "x,y": false, "": false, strings.Repeat("a", 32): false} {
		if serviceNameRE.MatchString(n) != want {
			t.Errorf("name %q: want valid=%v", n, want)
		}
	}
}

func TestIsServiceAccountDN(t *testing.T) {
	c := "ou=services,dc=x,dc=org"
	for dn, want := range map[string]bool{
		"cn=sssd,ou=services,dc=x,dc=org":             true,
		"CN=SSSD,OU=Services,DC=X,DC=ORG":             true,
		"ou=services,dc=x,dc=org":                     false, // the container itself
		"uid=alice,ou=people,dc=x,dc=org":             false,
		"cn=a,cn=b,ou=services,dc=x,dc=org":           false, // nested: not one of ours
		"cn=sssd,ou=services,dc=x,dc=org,dc=evil":     false,
		"cn=x,ou=notservices,dc=x,dc=org":             false,
		"uid=alice,ou=people,dc=x,dc=org,ou=services": false,
	} {
		if isServiceAccountDN(c, dn) != want {
			t.Errorf("%q: want %v", dn, want)
		}
	}
}

func TestHashSSHAChecksOut(t *testing.T) {
	h := hashSSHA("correct horse")
	if !strings.HasPrefix(h, "{SSHA}") {
		t.Fatalf("format: %s", h)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "{SSHA}"))
	if err != nil || len(raw) != 28 {
		t.Fatalf("decode: %v len %d", err, len(raw))
	}
	sum := sha1.Sum(append([]byte("correct horse"), raw[20:]...))
	if !reflect.DeepEqual(sum[:], raw[:20]) {
		t.Error("the hash is not SHA-1 of password+salt")
	}
	if hashSSHA("correct horse") == h {
		t.Error("two hashes of the same password should differ (random salt)")
	}
}

func TestInsertServiceRead(t *testing.T) {
	const box = "ou=services,dc=x,dc=org"
	osixia := []string{
		`{0}to * by dn.exact=gidNumber=0+uidNumber=0,cn=peercred,cn=external,cn=auth manage by * break`,
		`{1}to attrs=userPassword,shadowLastChange by self write by dn="cn=admin,dc=x,dc=org" write by anonymous auth by * none`,
		`{2}to * by self read by dn="cn=admin,dc=x,dc=org" write by * none`,
	}
	got, changed, err := insertServiceRead(osixia, box)
	if err != nil || !changed {
		t.Fatalf("err %v changed %v", err, changed)
	}
	if got[2] != `to * by dn.children="ou=services,dc=x,dc=org" read by self read by dn="cn=admin,dc=x,dc=org" write by * none` {
		t.Errorf("rule 3 = %s", got[2])
	}
	if got[0] != strings.TrimPrefix(osixia[0], "{0}") || got[1] != strings.TrimPrefix(osixia[1], "{1}") {
		t.Error("the other rules must be untouched, apart from losing their {n} numbers")
	}
	// the manage-and-break rule at the top must NOT be the one changed: it would expose password hashes
	if strings.Contains(got[0], "dn.children") {
		t.Error("the first 'to *' rule was changed")
	}
	// again: already there
	_, changed, err = insertServiceRead(got, box)
	if err != nil || changed {
		t.Errorf("second time: err %v changed %v", err, changed)
	}
	// no rule protects passwords before the catch-all: refuse
	if _, _, err := insertServiceRead([]string{`{0}to * by * read`}, box); err == nil || !strings.Contains(err.Error(), "password hashes") {
		t.Errorf("should refuse when nothing protects userPassword, got %v", err)
	}
	// no catch-all at all: refuse rather than guess
	if _, _, err := insertServiceRead([]string{`{0}to attrs=userPassword by self write by * none`, `{1}to dn.subtree="ou=people,dc=x,dc=org" by * read`}, box); err == nil {
		t.Error("should refuse when there is no catch-all rule")
	}
	// Dapper-run default: the catch-all already lets everyone read, the clause is still added harmlessly
	def := []string{`{0}to attrs=userPassword,shadowLastChange by self write by anonymous auth by * none`, `{1}to * by * read`}
	got, changed, err = insertServiceRead(def, box)
	if err != nil || !changed || got[1] != `to * by dn.children="ou=services,dc=x,dc=org" read by * read` {
		t.Errorf("default rules: %v %v %v", got, changed, err)
	}
}
