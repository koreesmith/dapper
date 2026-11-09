package ldapclient

import (
	"strings"
	"testing"
)

func TestParseObjectClassNames(t *testing.T) {
	got := ParseObjectClassNames([]string{
		"( 1.3.6.1.1.1.2.0 NAME 'posixAccount' DESC 'Abstraction of an account with POSIX attributes' SUP top AUXILIARY MUST ( cn $ uid ) )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) )",
		"( 1.2.3.4 NAME ( 'alpha' 'Beta' ) SUP top )",
		"( 1.2.3.5 DESC 'no name here' )",
		"( 1.3.6.1.4.1.24552.500.1.1.2.0 NAME 'ldapPublicKey' SUP top AUXILIARY )",
	})
	for _, want := range []string{"posixaccount", "person", "alpha", "beta", "ldappublickey"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	if got["shadowaccount"] || len(got) != 5 {
		t.Errorf("unexpected names: %v", got)
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"ldap://ldap.example.org:389": "ldap.example.org", "ldaps://ldap.example.org": "ldap.example.org",
		"ldap://[::1]:389": "::1", "ldap://10.0.0.5": "10.0.0.5", "not a url": "",
	} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSecretAttr(t *testing.T) {
	for _, secret := range []string{"userPassword", "USERPASSWORD", "sambaNTPassword", "sambaLMPassword", "sambaPasswordHistory",
		"olcRootPW", "pwdHistory", "krbPrincipalKey", "olcSyncrepl", "unicodePwd", "authPassword"} {
		if !IsSecretAttr(secret) {
			t.Errorf("%s should be treated as secret", secret)
		}
	}
	for _, ok := range []string{"cn", "uid", "mail", "gidNumber", "memberOf", "olcMirrorMode", "pwdAccountLockedTime", "pwdChangedTime", "sshPublicKey", "olcOverlay"} {
		if IsSecretAttr(ok) {
			t.Errorf("%s is not secret", ok)
		}
	}
}

func TestSyncreplCredentialsAreHidden(t *testing.T) {
	in := `rid=001 provider=ldap://a searchbase="dc=x" bindmethod=simple binddn="cn=r,dc=x" credentials=s3cret type=refreshOnly`
	got := syncreplSecret.ReplaceAllString(in, "${1}=<hidden>")
	if strings.Contains(got, "s3cret") || !strings.Contains(got, "credentials=<hidden>") || !strings.Contains(got, "type=refreshOnly") {
		t.Fatalf("unexpected: %s", got)
	}
	quoted := syncreplSecret.ReplaceAllString(`bindpw="a b c" rid=1`, "${1}=<hidden>")
	if strings.Contains(quoted, "a b c") {
		t.Fatalf("quoted secret survived: %s", quoted)
	}
}
