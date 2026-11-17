package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koreesmith/dapper/backend/internal/resets"
)

func resetServer(t *testing.T) *Server {
	t.Helper()
	s := mailServer(t)
	s.limit = newLimiter()
	s.resets = resets.NewStore()
	s.c().Password.MinLength = 8
	return s
}

func post(h http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.RemoteAddr = "203.0.113.9:5555"
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestForgotOnlyWhenMailIsOn(t *testing.T) {
	s := resetServer(t)
	w := httptest.NewRecorder()
	s.forgotStatus(w, httptest.NewRequest("GET", "/api/password/forgot", nil))
	if !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("should be enabled: %s", w.Body.String())
	}
	s.c().Mail.Enabled = false
	w = httptest.NewRecorder()
	s.forgotStatus(w, httptest.NewRequest("GET", "/api/password/forgot", nil))
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("mail off should disable it: %s", w.Body.String())
	}
	if w := post(s.forgot, "/api/password/forgot", map[string]string{"username": "ann"}); w.Code != http.StatusNotFound {
		t.Errorf("asking while mail is off should be refused, got %d", w.Code)
	}
}

func TestForgotAnswersTheSameForEveryone(t *testing.T) {
	s := resetServer(t)
	s.c().LDAP.URL = "ldap://127.0.0.1:1" // nothing there: the lookup happens after the answer and fails quietly
	a := post(s.forgot, "/api/password/forgot", map[string]string{"username": "ann"})
	b := post(s.forgot, "/api/password/forgot", map[string]string{"username": "nobody-at-all"})
	if a.Code != http.StatusOK || a.Body.String() != b.Body.String() {
		t.Fatalf("answers differ: %d %q vs %d %q", a.Code, a.Body.String(), b.Code, b.Body.String())
	}
	if w := post(s.forgot, "/api/password/forgot", map[string]string{"username": "  "}); w.Code != http.StatusBadRequest {
		t.Errorf("an empty name is a plain mistake, got %d", w.Code)
	}
}

func TestForgotIsRateLimited(t *testing.T) {
	s := resetServer(t)
	s.c().LDAP.URL = "ldap://127.0.0.1:1"
	same := ""
	for i := 0; i < 8; i++ { // past the per-account limit: still the same answer, so nothing reveals the account
		w := post(s.forgot, "/api/password/forgot", map[string]string{"username": "ann"})
		if w.Code != http.StatusOK {
			t.Fatalf("request %d got %d", i, w.Code)
		}
		if same == "" {
			same = w.Body.String()
		} else if w.Body.String() != same {
			t.Fatalf("answer changed after %d requests", i)
		}
	}
	var last int
	for i := 0; i < 25; i++ { // different names from one address hit the address limit
		last = post(s.forgot, "/api/password/forgot", map[string]string{"username": "u" + string(rune('a'+i))}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("one address asking again and again should be slowed down, got %d", last)
	}
}

func TestResetLinkChecks(t *testing.T) {
	s := resetServer(t)
	tok, _ := s.resets.Issue("uid=ann,ou=p,dc=x")
	if w := post(s.resetCheck, "/api/password/reset/check", map[string]string{"token": tok}); w.Code != http.StatusOK {
		t.Fatalf("a good link should check out: %d %s", w.Code, w.Body.String())
	}
	if w := post(s.resetCheck, "/api/password/reset/check", map[string]string{"token": "nope"}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("a bad link should be explained: %d %s", w.Code, w.Body.String())
	}
}

func TestWeakPasswordDoesNotUseUpTheLink(t *testing.T) {
	s := resetServer(t)
	tok, _ := s.resets.Issue("uid=ann,ou=p,dc=x")
	w := post(s.resetPasswordByLink, "/api/password/reset", map[string]string{"token": tok, "password": "short"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "at least 8") {
		t.Fatalf("the password rules should be explained: %d %s", w.Code, w.Body.String())
	}
	if _, ok := s.resets.Lookup(tok); !ok {
		t.Error("a typo must not cost the person their link")
	}
}

func TestGuessingLinksIsLimited(t *testing.T) {
	s := resetServer(t)
	var code int
	for i := 0; i < 25; i++ {
		code = post(s.resetPasswordByLink, "/api/password/reset", map[string]string{"token": "guess" + string(rune('a'+i)), "password": "longenoughpassword"}).Code
	}
	if code != http.StatusTooManyRequests {
		t.Errorf("repeated wrong links should be slowed down, got %d", code)
	}
}
