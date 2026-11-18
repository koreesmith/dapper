package resets

import (
	"testing"
	"time"
)

func TestLinkWorksOnce(t *testing.T) {
	s := NewStore()
	tok, err := s.Issue("uid=ann,ou=people,dc=x")
	if err != nil {
		t.Fatal(err)
	}
	if dn, ok := s.Lookup(tok); !ok || dn != "uid=ann,ou=people,dc=x" {
		t.Fatalf("lookup: %q %v", dn, ok)
	}
	if _, ok := s.Lookup(tok); !ok {
		t.Fatal("looking up must not use the link up")
	}
	if _, ok := s.Consume(tok); !ok {
		t.Fatal("first use should work")
	}
	if _, ok := s.Consume(tok); ok {
		t.Fatal("a link must work only once")
	}
}

func TestNewRequestReplacesTheOldLink(t *testing.T) {
	s := NewStore()
	a, _ := s.Issue("uid=ann,dc=x")
	b, _ := s.Issue("UID=Ann,dc=x") // same account, different spelling
	if _, ok := s.Lookup(a); ok {
		t.Error("the earlier link should stop working")
	}
	if _, ok := s.Lookup(b); !ok {
		t.Error("the newest link should work")
	}
	if a == b {
		t.Error("tokens must be random")
	}
}

func TestExpiryAndGarbage(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	tok, _ := s.Issue("uid=ann,dc=x")
	now = now.Add(TTL - time.Second)
	if _, ok := s.Lookup(tok); !ok {
		t.Error("still valid just before the hour is up")
	}
	now = now.Add(2 * time.Second)
	if _, ok := s.Lookup(tok); ok {
		t.Error("expired links must not work")
	}
	for _, g := range []string{"", "x", "AAAA", tok + "x"} {
		if _, ok := s.Consume(g); ok {
			t.Errorf("%q must not work", g)
		}
	}
}

func TestOnlyTheHashIsKept(t *testing.T) {
	s := NewStore()
	tok, _ := s.Issue("uid=ann,dc=x")
	for h := range s.byTok {
		if string(h[:]) == tok {
			t.Fatal("the token itself must not be stored")
		}
	}
	s.Revoke("uid=ann,dc=x")
	if _, ok := s.Lookup(tok); ok {
		t.Error("revoked links must not work")
	}
}

func TestLongerLifetimeForWelcomeLinks(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	tok, _ := s.IssueFor("uid=new,dc=x", 7*24*time.Hour)
	now = now.Add(6 * 24 * time.Hour)
	if _, ok := s.Lookup(tok); !ok {
		t.Error("a welcome link should still work after six days")
	}
	now = now.Add(2 * 24 * time.Hour)
	if _, ok := s.Lookup(tok); ok {
		t.Error("and not after eight")
	}
}
