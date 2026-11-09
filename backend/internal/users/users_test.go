package users

import (
	"fmt"
	"testing"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

func entry(uid, name, mail, phone string) ldapclient.Entry {
	return ldapclient.Entry{DN: "uid=" + uid + ",ou=people", Attrs: map[string][]string{
		"uid": {uid}, "cn": {name}, "displayName": {name}, "mail": {mail}, "telephoneNumber": {phone},
	}}
}

func sample() []User {
	return []User{
		FromEntry(entry("alice", "Alice Admin", "alice@example.org", "+1 555 0100")),
		FromEntry(entry("bob", "Bob Builder", "bob@example.org", "+1 (555) 0101")),
		FromEntry(entry("carol", "Carol Ng", "carol@corp.test", "")),
	}
}

func TestSearch(t *testing.T) {
	cases := map[string]int{
		"":              3,
		"ALICE":         1,
		"example.org":   2,
		"bob builder":   1,
		"builder bob":   1, // terms in any order
		"555-0101":      1, // phone ignores punctuation
		"5550100":       1,
		"+1 555":        2,
		"carol corp":    1,
		"nobody":        0,
		"alice builder": 0, // every term must match the same person
		"bk555":         0, // a name with letters doesn't match phone digits
		"alice 555":     1, // but a digits-only term still does, alongside a name
	}
	for q, want := range cases {
		if got := List(sample(), Query{Search: q}).Total; got != want {
			t.Errorf("search %q: got %d, want %d", q, got, want)
		}
	}
}

func TestSuspendedFlag(t *testing.T) {
	e := entry("dan", "Dan", "d@x", "")
	e.Attrs["pwdAccountLockedTime"] = []string{"20260101000000Z"}
	if !FromEntry(e).Suspended {
		t.Error("expected suspended")
	}
}

func TestPagingAndSort(t *testing.T) {
	var all []User
	for i := 0; i < 53; i++ {
		all = append(all, FromEntry(entry(fmt.Sprintf("u%02d", i), fmt.Sprintf("User %02d", i), "", "")))
	}
	p := List(all, Query{Page: 3, PageSize: 25})
	if p.Total != 53 || len(p.Users) != 3 || p.Users[0].Username != "u50" {
		t.Errorf("page 3: total=%d len=%d first=%v", p.Total, len(p.Users), p.Users[0].Username)
	}
	if p := List(all, Query{Page: 99, PageSize: 25}); len(p.Users) != 0 || p.Total != 53 {
		t.Error("out-of-range page should be empty with correct total")
	}
	d := List(all, Query{Sort: "username", Desc: true, PageSize: 5})
	if d.Users[0].Username != "u52" {
		t.Errorf("desc sort first = %s", d.Users[0].Username)
	}
	if p := List(all, Query{PageSize: 100000}); p.PageSize != 200 {
		t.Error("page size should be capped")
	}
}

func TestUsernameFallsBackToTheEntryName(t *testing.T) {
	e := ldapclient.Entry{DN: "cn=koree,ou=People,dc=kasasmith,dc=com", Attrs: map[string][]string{"cn": {"koree", "Koree Smith"}, "displayName": {"Koree Smith"}}}
	if u := FromEntry(e); u.Username != "koree" || u.Name != "Koree Smith" {
		t.Errorf("got %q / %q", u.Username, u.Name)
	}
	e2 := ldapclient.Entry{DN: `cn=a\,b,ou=p`, Attrs: map[string][]string{}}
	if got := FromEntry(e2).Username; got != "a,b" {
		t.Errorf("escaped comma: %q", got)
	}
}
