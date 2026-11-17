// Package mail sends Dapper's emails (password reset links and notices) through an SMTP server the
// administrator configures. It uses only the standard library.
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Security says how the connection to the mail server is protected.
const (
	StartTLS = "starttls" // connect in clear, then upgrade (usually port 587)
	TLS      = "tls"      // encrypted from the first byte (usually port 465)
	None     = "none"     // no encryption; refused for anything but this machine
)

// Settings is how to reach the mail server and who the mail is from.
type Settings struct {
	Host       string
	Port       int
	Security   string
	Username   string
	Password   string
	From       string // address
	FromName   string
	SkipVerify bool // accept a certificate that isn't trusted (not recommended)
}

// Timeout bounds a whole send.
var Timeout = 20 * time.Second

// Validate checks the settings are complete enough to try.
func (s Settings) Validate() error {
	switch {
	case strings.TrimSpace(s.Host) == "":
		return errors.New("enter the mail server's address")
	case s.Port < 1 || s.Port > 65535:
		return errors.New("the mail server's port must be between 1 and 65535")
	case s.Security != StartTLS && s.Security != TLS && s.Security != None:
		return errors.New("choose how the connection to the mail server is encrypted")
	}
	if _, err := mail.ParseAddress(s.From); err != nil {
		return errors.New("enter a valid “from” email address")
	}
	if s.Security == None && !isLocal(s.Host) {
		return errors.New("an unencrypted connection is only allowed to a mail server on this machine; choose STARTTLS or TLS")
	}
	if strings.ContainsAny(s.Host+s.Username+s.FromName, "\r\n") {
		return errors.New("settings can't contain line breaks")
	}
	return nil
}

func isLocal(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Message is one email. Body is plain text.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Build renders the message as RFC 5322 text. It refuses anything that could add headers.
func Build(s Settings, m Message) ([]byte, error) {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return nil, errors.New("that isn't a valid email address")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("the subject can't contain line breaks")
	}
	from := mail.Address{Name: s.FromName, Address: s.From}
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + encodeSubject(m.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String()), nil
}

func encodeSubject(s string) string {
	for _, r := range s {
		if r > 126 {
			return mime.QEncoding.Encode("utf-8", s)
		}
	}
	return s
}

// Send delivers one message. Errors are phrased for the person running Dapper.
func Send(s Settings, m Message) error {
	if err := s.Validate(); err != nil {
		return err
	}
	msg, err := Build(s, m)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(strings.TrimSpace(s.Host), strconv.Itoa(s.Port))
	d := net.Dialer{Timeout: Timeout}
	cfg := &tls.Config{ServerName: strings.TrimSpace(s.Host), MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.SkipVerify} // #nosec G402 -- opt-in, shown as not recommended
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("couldn't reach the mail server at %s: check the address, port and network access", addr)
	}
	_ = conn.SetDeadline(time.Now().Add(Timeout))
	if s.Security == TLS {
		tc := tls.Client(conn, cfg)
		if err := tc.Handshake(); err != nil {
			conn.Close()
			return fmt.Errorf("the mail server's secure connection failed (%s); check the port and encryption setting, and that its certificate is trusted", short(err))
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, strings.TrimSpace(s.Host))
	if err != nil {
		conn.Close()
		return errors.New("the server didn't answer like a mail server; check the port and encryption setting")
	}
	defer c.Close()
	if s.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server doesn't offer STARTTLS; choose another encryption setting")
		}
		if err := c.StartTLS(cfg); err != nil {
			return fmt.Errorf("the mail server's secure connection failed (%s); check that its certificate is trusted", short(err))
		}
	}
	if s.Username != "" {
		if s.Security == None && !isLocal(s.Host) {
			return errors.New("won't send a password over an unencrypted connection")
		}
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("the mail server doesn't accept a sign-in on this connection; clear the username or change the encryption setting")
		}
		if err := c.Auth(plainAuth{s.Username, s.Password}); err != nil {
			return errors.New("the mail server rejected the username or password")
		}
	}
	from, _ := mail.ParseAddress(s.From)
	to, _ := mail.ParseAddress(m.To)
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("the mail server refused the sender address: %s", short(err))
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("the mail server refused the recipient: %s", short(err))
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("the mail server refused the message: %s", short(err))
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("sending the message failed: %s", short(err))
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail server didn't accept the message: %s", short(err))
	}
	_ = c.Quit()
	return nil
}

func short(err error) string {
	t := err.Error()
	if len(t) > 160 {
		t = t[:160]
	}
	return t
}

// plainAuth is PLAIN/LOGIN over a connection already known to be protected (or local). The standard
// library's PlainAuth refuses non-TLS hosts other than localhost, which is the rule we want, but it also
// insists on a matching host name, so we do the exchange ourselves.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}
