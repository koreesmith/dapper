package api

import (
	"errors"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

func me(dn string, attrs map[string][]string) ldapclient.Entry {
	return ldapclient.Entry{DN: dn, Attrs: attrs}
}

func TestHashScheme(t *testing.T) {
	for in, want := range map[string]string{
		"{SSHA}abcd": "SSHA", "{ssha512}x": "SSHA512", "{CRYPT}$6$x": "CRYPT", "{ARGON2}$argon2id$v=19": "ARGON2",
		"plain": "", "": "", "{}x": "", "{unterminated": "",
	} {
		if got := hashScheme(in); got != want {
			t.Errorf("hashScheme(%q) = %q, want %q", in, got, want)
		}
	}
	if !supportedHashes["SSHA"] || !supportedHashes["PBKDF2-SHA256"] || supportedHashes["NS-MTA-MD5"] {
		t.Error("supported hash table is wrong")
	}
}

func TestOrderForCopyPutsParentsFirstAndGroupsLast(t *testing.T) {
	es := []ldapclient.Entry{
		me("cn=staff,ou=groups,dc=x,dc=org", map[string][]string{"objectClass": {"groupOfNames"}}),
		me("uid=ann,ou=people,dc=x,dc=org", map[string][]string{"objectClass": {"inetOrgPerson"}}),
		me("ou=people,dc=x,dc=org", map[string][]string{"objectClass": {"organizationalUnit"}}),
		me("dc=x,dc=org", map[string][]string{"objectClass": {"dcObject", "organization"}}),
		me("ou=groups,dc=x,dc=org", map[string][]string{"objectClass": {"organizationalUnit"}}),
	}
	var got []string
	for _, e := range orderForCopy(es) {
		got = append(got, e.DN)
	}
	want := []string{"dc=x,dc=org", "ou=people,dc=x,dc=org", "ou=groups,dc=x,dc=org", "uid=ann,ou=people,dc=x,dc=org", "cn=staff,ou=groups,dc=x,dc=org"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order:\n got %v\nwant %v", got, want)
	}
}

func TestDiffAttrs(t *testing.T) {
	want := map[string][]string{"cn": {"Ann"}, "mail": {"a@x.org", "b@x.org"}, "userPassword": {"{SSHA}x"}}
	same := map[string][]string{"CN": {"Ann"}, "mail": {"b@x.org", "a@x.org"}, "userpassword": {"{SSHA}x"}}
	if ch := diffAttrs(want, same); len(ch) != 0 {
		t.Errorf("identical entries (names in any case, values in any order) should have no changes, got %v", ch)
	}
	have := map[string][]string{"cn": {"Ann"}, "mail": {"a@x.org"}, "title": {"Old"}}
	ch := diffAttrs(want, have)
	got := map[string]string{}
	for _, c := range ch {
		got[c.Attr] = c.Op
	}
	if !reflect.DeepEqual(got, map[string]string{"mail": "replace", "userPassword": "replace", "title": "delete"}) {
		t.Errorf("changes = %v", got)
	}
}

func TestUnsupportedFor(t *testing.T) {
	classes := map[string]bool{"top": true, "inetorgperson": true, "person": true}
	attrs := map[string]bool{"cn": true, "sn": true, "userpassword": true, "usercertificate": true}
	e := me("uid=a,dc=x", map[string][]string{
		"objectClass": {"inetOrgPerson", "sudoRole"}, "cn": {"A"}, "sn": {"A"}, "sudoUser": {"a"}, "userCertificate;binary": {"x"},
	})
	cls, att := unsupportedFor(e, classes, attrs)
	if !reflect.DeepEqual(cls, []string{"sudoRole"}) || !reflect.DeepEqual(att, []string{"sudoUser"}) {
		t.Errorf("classes %v attributes %v", cls, att)
	}
}

func TestKindsAndChecklist(t *testing.T) {
	if migKind(me("a", map[string][]string{"objectClass": {"posixGroup"}})) != "groups" ||
		migKind(me("a", map[string][]string{"objectClass": {"inetOrgPerson"}})) != "people" ||
		migKind(me("a", map[string][]string{"objectClass": {"organizationalUnit"}})) != "units" ||
		migKind(me("a", map[string][]string{"objectClass": {"device"}})) != "other" {
		t.Error("kinds")
	}
	if migDepth("uid=a\\,b,ou=people,dc=x,dc=org") != 4 {
		t.Errorf("escaped commas must not count as separators, got %d", migDepth("uid=a\\,b,ou=people,dc=x,dc=org"))
	}
	l := cutoverChecklist("localhost", "dc=x,dc=org", &migResult{Skipped: 2})
	joined := strings.Join(l, "\n")
	for _, want := range []string{"old directory manager", "refuses unencrypted", "err=49"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the checklist should mention %q", want)
		}
	}
	if len(l) < 8 || l[0][:20] != "The new server's cer" {
		t.Errorf("checklist should warn about a localhost certificate first, got %q", l[0])
	}
}

func TestDiagnoseNoHandoff(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DAPPER_HANDOFF_DIR", dir+"/missing") // not mounted at all
	t.Setenv("DAPPER_BUNDLED_URL", "ldaps://127.0.0.1:1")
	if r, _ := diagnoseNoHandoff(os.ErrNotExist); r != "no-volume" {
		t.Errorf("a missing shared folder should say the server wasn't started with Dapper, got %q", r)
	}
	t.Setenv("DAPPER_HANDOFF_DIR", dir) // mounted, empty, nothing listening
	if r, _ := diagnoseNoHandoff(os.ErrNotExist); r != "server-down" {
		t.Errorf("a shared folder but no server should say it isn't running, got %q", r)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0") // something is listening, but no first-start file
	if err != nil {
		t.Skip("can't listen")
	}
	defer ln.Close()
	t.Setenv("DAPPER_BUNDLED_URL", "ldaps://"+ln.Addr().String())
	if r, m := diagnoseNoHandoff(os.ErrNotExist); r != "already-used" || !strings.Contains(m, "only once") {
		t.Errorf("a running server with no first-start file means it was already used, got %q: %s", r, m)
	}
	if r, _ := diagnoseNoHandoff(errors.New("permission denied")); r != "unusable" {
		t.Errorf("an unreadable file should be reported as such, got %q", r)
	}
}
