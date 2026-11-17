package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/mail"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// mailIO is the mail settings as the browser sees them: the password is accepted but never returned.
type mailIO struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
	From        string `json:"from"`
	FromName    string `json:"fromName"`
	SkipVerify  bool   `json:"skipVerify"`
	PublicURL   string `json:"publicURL"`
}

func mailToIO(m config.Mail) mailIO {
	return mailIO{Enabled: m.Enabled, Host: m.Host, Port: m.Port, Security: m.Security, Username: m.Username, PasswordSet: m.Password != "",
		From: m.From, FromName: m.FromName, SkipVerify: m.SkipVerify, PublicURL: m.PublicURL}
}

// mailFromIO builds the candidate settings. A blank password keeps the saved one only while the server, port and
// username are unchanged, so a saved password can never be sent to a different server.
func mailFromIO(cur config.Mail, o mailIO) config.Mail {
	m := config.Mail{
		Enabled: o.Enabled, Host: strings.TrimSpace(o.Host), Port: o.Port, Security: strings.TrimSpace(o.Security),
		Username: strings.TrimSpace(o.Username), Password: o.Password, From: strings.TrimSpace(o.From),
		FromName: strings.TrimSpace(o.FromName), SkipVerify: o.SkipVerify, PublicURL: strings.TrimRight(strings.TrimSpace(o.PublicURL), "/"),
	}
	same := strings.EqualFold(m.Host, cur.Host) && m.Port == cur.Port && m.Username == cur.Username
	if m.Password == "" && same {
		m.Password = cur.Password
	}
	if m.Username == "" {
		m.Password = ""
	}
	return m
}

// MailSettings converts saved settings for the sender.
func mailSettings(m config.Mail) mail.Settings {
	return mail.Settings{Host: m.Host, Port: m.Port, Security: m.Security, Username: m.Username, Password: m.Password,
		From: m.From, FromName: m.FromName, SkipVerify: m.SkipVerify}
}

func (s *Server) mailGet(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	writeJSON(w, map[string]any{"mail": mailToIO(s.c().Mail)})
}

func (s *Server) mailPut(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var o mailIO
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cur := s.c()
	next := *cur
	next.Mail = mailFromIO(cur.Mail, o)
	if next.Mail.Port == 0 {
		next.Mail.Port = 587
	}
	if err := next.Validate(); err != nil {
		fail(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	if err := s.store.Save(&next); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error())
		return
	}
	audit(sess, "mail.update", "settings", "enabled", map[bool]string{true: "yes", false: "no"}[next.Mail.Enabled])
	writeJSON(w, map[string]any{"mail": mailToIO(next.Mail)})
}

// mailTest sends a message with the settings on the page (saved or not) to an address the administrator types.
func (s *Server) mailTest(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		mailIO
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	m := mailFromIO(s.c().Mail, in.mailIO)
	if m.Port == 0 {
		m.Port = 587
	}
	err := mail.Send(mailSettings(m), mail.Message{
		To:      strings.TrimSpace(in.To),
		Subject: "Dapper test message",
		Body:    "This is a test message from Dapper.\n\nIf you can read it, Dapper can send email through the server you set up.\n",
	})
	if err != nil {
		audit(sess, "mail.test.failed", "settings")
		fail(w, http.StatusBadRequest, capitalize(err.Error())+".")
		return
	}
	audit(sess, "mail.test", "settings")
	writeJSON(w, map[string]string{"status": "sent"})
}
