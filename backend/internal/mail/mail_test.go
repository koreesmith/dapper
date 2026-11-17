package mail

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeSMTP is a minimal plain-text SMTP server that records what it is sent.
func fakeSMTP(t *testing.T, authOK bool) (port int, got chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { c.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					got <- data.String()
					w("250 queued")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				w("250-fake")
				w("250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH"):
				if authOK {
					w("235 ok")
				} else {
					w("535 no")
				}
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				w("250 ok")
			case line == "DATA":
				inData = true
				w("354 go")
			case line == "QUIT":
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, got
}

func base(port int) Settings {
	return Settings{Host: "127.0.0.1", Port: port, Security: None, From: "dapper@example.org", FromName: "Dapper"}
}

func TestSendDelivers(t *testing.T) {
	port, got := fakeSMTP(t, true)
	s := base(port)
	s.Username, s.Password = "u", "p"
	if err := Send(s, Message{To: "ann@example.org", Subject: "Hello", Body: "line one\nline two"}); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"From: \"Dapper\" <dapper@example.org>", "To: <ann@example.org>", "Subject: Hello", "line one", "line two"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestSendBadPasswordIsExplained(t *testing.T) {
	port, _ := fakeSMTP(t, false)
	s := base(port)
	s.Username, s.Password = "u", "wrong"
	err := Send(s, Message{To: "ann@example.org", Subject: "x", Body: "y"})
	if err == nil || !strings.Contains(err.Error(), "username or password") {
		t.Fatalf("want a username/password message, got %v", err)
	}
}

func TestUnreachableServerIsExplained(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	err := Send(base(port), Message{To: "ann@example.org", Subject: "x", Body: "y"})
	if err == nil || !strings.Contains(err.Error(), "couldn't reach") {
		t.Fatalf("got %v", err)
	}
}

func TestHeaderInjectionRefused(t *testing.T) {
	s := base(25)
	if _, err := Build(s, Message{To: "ann@example.org", Subject: "hi\r\nBcc: evil@example.org", Body: "x"}); err == nil {
		t.Error("a subject with a line break must be refused")
	}
	if _, err := Build(s, Message{To: "ann@example.org\r\nBcc: evil@example.org", Subject: "x", Body: "x"}); err == nil {
		t.Error("a recipient with a line break must be refused")
	}
	out, err := Build(s, Message{To: "ann@example.org", Subject: "café", Body: "x"})
	if err != nil || !strings.Contains(string(out), "=?utf-8?q?caf=C3=A9?=") {
		t.Errorf("non-ASCII subject should be encoded: %v\n%s", err, out)
	}
}

func TestValidate(t *testing.T) {
	ok := Settings{Host: "smtp.example.org", Port: 587, Security: StartTLS, From: "a@example.org"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Settings){
		"no host":            func(s *Settings) { s.Host = "" },
		"bad port":           func(s *Settings) { s.Port = 0 },
		"bad from":           func(s *Settings) { s.From = "nope" },
		"unencrypted remote": func(s *Settings) { s.Security = None },
		"unknown security":   func(s *Settings) { s.Security = "ssl3" },
		"line break in host": func(s *Settings) { s.Host = "a\nb" },
	} {
		s := ok
		mut(&s)
		if s.Validate() == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}
