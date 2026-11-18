package api

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/koreesmith/dapper/backend/internal/mail"
)

func welcomeServer(t *testing.T, sent *[]mail.Message, fail error) *Server {
	t.Helper()
	s := resetServer(t)
	s.c().Mail.FromName = "KasaSmith Dapper"
	s.sender = func(m mail.Message) error {
		*sent = append(*sent, m) // recorded even when the send fails, so a test can look at the link
		return fail
	}
	return s
}

func TestWelcomeEmailHasUsernameAndASevenDayLinkButNoPassword(t *testing.T) {
	var sent []mail.Message
	s := welcomeServer(t, &sent, nil)
	dn := "uid=ann,ou=p,dc=x"
	ok, msg := s.sendWelcome(dn, "ann", map[string][]string{"mail": {"ann@example.org"}, "givenName": {"Ann"}})
	if !ok || len(sent) != 1 {
		t.Fatalf("should be sent: %v %q", ok, msg)
	}
	m := sent[0]
	if m.To != "ann@example.org" || m.Subject != "Welcome to KasaSmith Dapper" {
		t.Errorf("to/subject: %q %q", m.To, m.Subject)
	}
	for _, want := range []string{"Hello Ann,", "Your username is: ann", "within the next 7 days", "https://dapper.example.org"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
	tok := regexp.MustCompile(`#/reset/([A-Za-z0-9_-]+)`).FindStringSubmatch(m.Body)
	if tok == nil {
		t.Fatalf("no link in:\n%s", m.Body)
	}
	if got, ok := s.resets.Lookup(tok[1]); !ok || got != dn {
		t.Errorf("the link should work for the new account: %q %v", got, ok)
	}
}

func TestWelcomeSaysWhyItWasNotSent(t *testing.T) {
	var sent []mail.Message
	s := welcomeServer(t, &sent, nil)
	if ok, msg := s.sendWelcome("uid=a,dc=x", "a", map[string][]string{}); ok || !strings.Contains(msg, "no email address") {
		t.Errorf("no address: %v %q", ok, msg)
	}
	s.c().Mail.Enabled = false
	if ok, msg := s.sendWelcome("uid=a,dc=x", "a", map[string][]string{"mail": {"a@example.org"}}); ok || !strings.Contains(msg, "Email isn't set up") {
		t.Errorf("mail off: %v %q", ok, msg)
	}
	if len(sent) != 0 {
		t.Error("nothing should have been sent")
	}
}

func TestWelcomeFailureLeavesNoLiveLink(t *testing.T) {
	var sent []mail.Message
	s := welcomeServer(t, &sent, errors.New("the mail server rejected the username or password"))
	ok, msg := s.sendWelcome("uid=a,dc=x", "a", map[string][]string{"mail": {"a@example.org"}})
	if ok || !strings.Contains(msg, "rejected") {
		t.Errorf("should say why: %v %q", ok, msg)
	}
	if len(sent) != 1 {
		t.Fatalf("the send should have been attempted once, got %d", len(sent))
	}
	tok := regexp.MustCompile(`#/reset/([A-Za-z0-9_-]+)`).FindStringSubmatch(sent[0].Body)
	if tok == nil {
		t.Fatal("no link in the attempted message")
	}
	if _, found := s.resets.Lookup(tok[1]); found {
		t.Error("a link that never reached the person must not stay valid")
	}
}
