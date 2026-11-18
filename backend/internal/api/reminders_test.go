package api

import (
	"strings"
	"testing"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/mail"
)

func TestExpiryEmailSaysWhenAndHowToChangeIt(t *testing.T) {
	var sent []mail.Message
	s := welcomeServer(t, &sent, nil)
	e := ldapclient.Entry{DN: "uid=ann,ou=p,dc=x", Attrs: map[string][]string{"mail": {"ann@example.org"}, "uid": {"ann"}, "givenName": {"Ann"}}}
	expiry := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !s.sendExpiryEmail(e, expiry, 7) || len(sent) != 1 {
		t.Fatal("should be sent")
	}
	m := sent[0]
	if m.Subject != "Your KasaSmith Dapper password expires in 7 days" || m.To != "ann@example.org" {
		t.Errorf("subject/to: %q %q", m.Subject, m.To)
	}
	for _, want := range []string{"Hello Ann,", "\"ann\"", "expires in 7 days", "Change password", "Forgot your password?", "https://dapper.example.org"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
	sent = nil
	if !s.sendExpiryEmail(e, expiry, 1) || sent[0].Subject != "Your KasaSmith Dapper password expires tomorrow" {
		t.Errorf("the last day should say tomorrow: %v", sent)
	}
}

func TestExpiryEmailNotSentWithoutAnAddressOrWhenTheServerFails(t *testing.T) {
	var sent []mail.Message
	s := welcomeServer(t, &sent, nil)
	if s.sendExpiryEmail(ldapclient.Entry{DN: "uid=a,dc=x", Attrs: map[string][]string{}}, time.Now(), 3) || len(sent) != 0 {
		t.Error("no address, nothing to send")
	}
	s2 := welcomeServer(t, &sent, errTest("rejected"))
	if s2.sendExpiryEmail(ldapclient.Entry{DN: "uid=a,dc=x", Attrs: map[string][]string{"mail": {"a@example.org"}}}, time.Now(), 3) {
		t.Error("a failed send must be reported as not sent, so it is tried again at the next check")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
