package ldapclient

import "testing"

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
