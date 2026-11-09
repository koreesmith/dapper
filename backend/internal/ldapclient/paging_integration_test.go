package ldapclient

import (
	"os"
	"strings"
	"testing"
)

// Runs only when DAPPER_TEST_LDAP_URL points at a directory with the demo seed data.
//
//	DAPPER_TEST_LDAP_URL=ldap://localhost:389 go test ./internal/ldapclient/
func TestPagingReturnsEveryone(t *testing.T) {
	url := os.Getenv("DAPPER_TEST_LDAP_URL")
	if url == "" {
		t.Skip("DAPPER_TEST_LDAP_URL not set")
	}
	c, err := Dial(Server{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Bind("cn=admin,dc=example,dc=org", "admin"); err != nil {
		t.Fatal(err)
	}
	base := "ou=people,dc=example,dc=org"

	pageSize = 250
	all, _, err := c.ListPeople(base, []string{"uid"}, 10000)
	if err != nil || len(all) < 30 {
		t.Fatalf("baseline: %d entries, %v", len(all), err)
	}
	for _, size := range []uint32{1, 3, 7, 10} {
		pageSize = size
		got, _, err := c.ListPeople(base, []string{"uid"}, 10000)
		if err != nil || len(got) != len(all) {
			t.Errorf("pageSize %d returned %d of %d (%v)", size, len(got), len(all), err)
		}
		seen := map[string]bool{}
		for _, e := range got {
			if seen[e.DN] {
				t.Errorf("pageSize %d returned %s twice", size, e.DN)
			}
			seen[e.DN] = true
		}
	}
	// The cap must stop early and report truncation.
	pageSize = 4
	got, trunc, _ := c.ListPeople(base, []string{"uid"}, 10)
	if len(got) != 10 || !trunc {
		t.Errorf("limit 10: got %d truncated=%v", len(got), trunc)
	}
	pageSize = 250
	// SearchComplete sees everything.
	es, err := c.SearchComplete(base, "(uid=*)", []string{"uid"})
	if err != nil || len(es) != len(all) {
		t.Errorf("SearchComplete: %d vs %d (%v)", len(es), len(all), err)
	}
	names, err := c.ObjectClassNames()
	if err != nil || !names["posixaccount"] || !names["inetorgperson"] {
		t.Errorf("schema names: %v %v", len(names), err)
	}
}

// StartTLS with verification on must fail because the certificate isn't trusted, never because
// no server name was given (which used to force people to turn verification off).
func TestStartTLSChecksTheCertificate(t *testing.T) {
	url := os.Getenv("DAPPER_TEST_LDAP_URL")
	if url == "" {
		t.Skip("DAPPER_TEST_LDAP_URL not set")
	}
	_, err := Dial(Server{URL: url, StartTLS: true})
	if err == nil {
		t.Skip("the server's certificate is trusted here")
	}
	if strings.Contains(err.Error(), "either ServerName or InsecureSkipVerify") {
		t.Fatalf("server name isn't being set: %v", err)
	}
	// (Skipping verification should connect, but the demo image's built-in certificate has expired and
	// its TLS doesn't work for any client, so that half is only informational here.)
	if c, err := Dial(Server{URL: url, StartTLS: true, SkipVerify: true}); err != nil {
		t.Logf("skip-verify connect failed (the demo server's TLS is broken): %v", err)
	} else {
		c.Close()
	}
}
