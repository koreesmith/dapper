package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koreesmith/dapper/backend/internal/config"
)

func mailServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{}
	cfg.LDAP.URL, cfg.LDAP.BindDN, cfg.LDAP.BindPassword = "ldaps://x", "cn=a", "pw"
	cfg.Users.BaseDN, cfg.Admin.GroupDN = "ou=p,dc=x", "cn=admins,dc=x"
	cfg.Mail = config.Mail{Enabled: true, Host: "smtp.example.org", Port: 587, Security: "starttls", Username: "u", Password: "secret",
		From: "dapper@example.org", PublicURL: "https://dapper.example.org"}
	return &Server{store: config.Static(cfg)}
}

func putMail(t *testing.T, s *Server, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	s.mailPut(w, httptest.NewRequest("PUT", "/api/admin/mail", bytes.NewReader(b)), nil)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestMailGetNeverReturnsThePassword(t *testing.T) {
	s := mailServer(t)
	w := httptest.NewRecorder()
	s.mailGet(w, httptest.NewRequest("GET", "/api/admin/mail", nil), nil)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("the saved password leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"passwordSet":true`) {
		t.Errorf("should say a password is set: %s", w.Body.String())
	}
}

func TestMailBlankPasswordKeepsSavedOnlyForSameServer(t *testing.T) {
	s := mailServer(t)
	cur := s.c().Mail
	same := mailFromIO(cur, mailIO{Host: "SMTP.example.org", Port: 587, Username: "u"})
	if same.Password != "secret" {
		t.Errorf("same server and user should keep the password, got %q", same.Password)
	}
	for name, o := range map[string]mailIO{
		"other host": {Host: "evil.example.net", Port: 587, Username: "u"},
		"other port": {Host: "smtp.example.org", Port: 25, Username: "u"},
		"other user": {Host: "smtp.example.org", Port: 587, Username: "v"},
	} {
		if got := mailFromIO(cur, o).Password; got != "" {
			t.Errorf("%s: saved password must not follow, got %q", name, got)
		}
	}
	if got := mailFromIO(cur, mailIO{Host: "smtp.example.org", Port: 587}).Password; got != "" {
		t.Errorf("no username means no password, got %q", got)
	}
}

func TestMailPutSavesAndValidates(t *testing.T) {
	s := mailServer(t)
	good := mailIO{Enabled: true, Host: "mail.example.org", Port: 465, Security: "tls", From: "no-reply@example.org", PublicURL: "https://dapper.example.org/"}
	w, out := putMail(t, s, good)
	if w.Code != http.StatusOK {
		t.Fatalf("good settings refused: %s", w.Body.String())
	}
	if m := s.c().Mail; m.Host != "mail.example.org" || m.PublicURL != "https://dapper.example.org" {
		t.Errorf("not saved as expected: %+v", m)
	}
	if strings.Contains(w.Body.String(), "password\":\"") {
		t.Errorf("response must not carry a password: %v", out)
	}
	for name, bad := range map[string]mailIO{
		"no address":         {Enabled: true, Host: "mail.example.org", Port: 587, Security: "starttls", From: "a@example.org", PublicURL: "https://d.example.org"},
		"no web address":     {Enabled: true, Host: "mail.example.org", Port: 587, Security: "starttls", From: "a@example.org"},
		"clear text, remote": {Enabled: true, Host: "mail.example.org", Port: 25, Security: "none", From: "a@example.org", PublicURL: "https://d.example.org"},
		"bad from":           {Enabled: true, Host: "mail.example.org", Port: 587, Security: "starttls", From: "nope", PublicURL: "https://d.example.org"},
	} {
		if name == "no address" {
			bad.From = "" // a missing sender
		}
		if w, _ := putMail(t, s, bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s: should be refused, got %d", name, w.Code)
		}
	}
	// Turned off, incomplete settings are fine to save.
	if w, _ := putMail(t, s, mailIO{Enabled: false, Port: 587, Security: "starttls"}); w.Code != http.StatusOK {
		t.Errorf("turning mail off should always save: %s", w.Body.String())
	}
}

func TestMailTestReportsFailureInPlainWords(t *testing.T) {
	s := mailServer(t)
	b, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": 1, "security": "none", "from": "a@example.org", "to": "ann@example.org"})
	w := httptest.NewRecorder()
	s.mailTest(w, httptest.NewRequest("POST", "/api/admin/mail/test", bytes.NewReader(b)), nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Couldn't reach") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
