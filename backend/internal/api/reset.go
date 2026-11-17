package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/mail"
)

// Forgotten passwords. A person asks for a link (by username or email), Dapper emails a one-time link to the address
// the directory holds for them, and the link lets them choose a new password. Nothing a stranger can see depends on
// whether the account exists: the answer is always the same, and the work happens after the answer is sent.

const forgotAnswer = "If that matches an account that has an email address on file, a link to choose a new password is on its way. It works once and expires in an hour."

// resetEnabled says whether people can ask for a link: mail must be set up and turned on.
func (s *Server) resetEnabled() bool {
	m := s.c().Mail
	return s.store.Configured() && m.Enabled && m.PublicURL != ""
}

func (s *Server) forgotStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"enabled": s.resetEnabled()})
}

func (s *Server) forgot(w http.ResponseWriter, r *http.Request) {
	if !s.resetEnabled() {
		fail(w, http.StatusNotFound, "Password reset by email isn't set up. Ask an administrator to reset your password.")
		return
	}
	var in struct{ Username string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	who := strings.TrimSpace(in.Username)
	if who == "" || len(who) > 256 {
		fail(w, http.StatusBadRequest, "Enter your username or email address.")
		return
	}
	// Every request counts, found or not, so the limits can't be used to tell accounts apart.
	ipKey, userKey := "ip:forgot:"+clientIP(r), "forgot:"+strings.ToLower(who)
	if s.limit.blocked(ipKey) {
		audit(nil, "password.forgot.blocked", who, "ip", clientIP(r))
		w.Header().Set("Retry-After", "600")
		fail(w, http.StatusTooManyRequests, "Too many requests. Wait a few minutes and try again.")
		return
	}
	skip := s.limit.blocked(userKey) // looks the same from outside; just nothing is sent
	s.limit.fail(ipKey, userKey)
	ip := clientIP(r)
	writeJSON(w, map[string]string{"message": forgotAnswer})
	if skip {
		audit(nil, "password.forgot.blocked", who, "ip", ip)
		return
	}
	go s.sendResetLink(who, ip)
}

// sendResetLink finds the account and emails the link. It runs after the response, so how long it takes says nothing.
func (s *Server) sendResetLink(who, ip string) {
	cfg := s.c()
	svc, err := dialSvc(cfg)
	if err != nil {
		log.Printf("password reset: directory: %v", err)
		return
	}
	defer svc.Close()
	dn, err := svc.FindUserDN(cfg.Users.BaseDN, cfg.Users.LoginAttributes, who)
	if err != nil || s.isSuspendedDN(dn) {
		audit(nil, "password.forgot", who, "ip", ip, "result", "no such account")
		return
	}
	e, err := svc.GetAttrs(dn, []string{"mail", "pwdAccountLockedTime"})
	if err == nil && len(e.Values("pwdAccountLockedTime")) > 0 { // suspended by locking
		audit(nil, "password.forgot", dn, "ip", ip, "result", "account suspended")
		return
	}
	if err != nil || len(e.Values("mail")) == 0 || strings.TrimSpace(e.Values("mail")[0]) == "" {
		audit(nil, "password.forgot", dn, "ip", ip, "result", "no email address on file")
		return
	}
	to := strings.TrimSpace(e.Values("mail")[0])
	tok, err := s.resets.Issue(dn)
	if err != nil {
		log.Printf("password reset: %v", err)
		return
	}
	link := cfg.Mail.PublicURL + "/#/reset/" + tok
	err = s.send(cfg.Mail, mail.Message{To: to, Subject: "Choose a new password", Body: "Someone asked to choose a new password for your account" +
		" (if that wasn't you, you can ignore this message and nothing will change).\n\n" +
		"To choose a new password, open this link. It works once and expires in an hour:\n\n" + link + "\n"})
	if err != nil {
		s.resets.Revoke(dn)
		log.Printf("password reset: sending to %s failed: %v", to, err)
		audit(nil, "password.forgot", dn, "ip", ip, "result", "email failed")
		return
	}
	audit(nil, "password.forgot", dn, "ip", ip, "result", "link sent")
}

// send delivers one email with the saved settings (tests replace s.sender).
func (s *Server) send(m config.Mail, msg mail.Message) error {
	if s.sender != nil {
		return s.sender(msg)
	}
	return mail.Send(mailSettings(m), msg)
}

// resetCheck says whether a link is still good, so the page can say so before anyone types a password.
func (s *Server) resetCheck(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	ipKey := "ip:reset:" + clientIP(r)
	if s.limit.blocked(ipKey) {
		w.Header().Set("Retry-After", "600")
		fail(w, http.StatusTooManyRequests, "Too many attempts. Wait a few minutes and try again.")
		return
	}
	dn, ok := s.resets.Lookup(in.Token)
	if !ok || s.isSuspendedDN(dn) {
		s.limit.fail(ipKey)
		fail(w, http.StatusBadRequest, linkExpired)
		return
	}
	writeJSON(w, map[string]any{"valid": true, "minLength": s.c().Password.MinLength})
}

var errSuspended = validationError(linkExpired)

const linkExpired = "This link has expired or was already used. Ask for a new one from the sign-in page."

// resetPasswordByLink sets the new password. It signs nobody in: the person signs in with it afterwards.
func (s *Server) resetPasswordByLink(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	ipKey := "ip:reset:" + clientIP(r)
	if s.limit.blocked(ipKey) {
		w.Header().Set("Retry-After", "600")
		fail(w, http.StatusTooManyRequests, "Too many attempts. Wait a few minutes and try again.")
		return
	}
	dn, ok := s.resets.Lookup(in.Token)
	if !ok || s.isSuspendedDN(dn) {
		s.limit.fail(ipKey)
		audit(nil, "password.reset.refused", "-", "ip", clientIP(r))
		fail(w, http.StatusBadRequest, linkExpired)
		return
	}
	// The password has to pass the rules before the link is used up, so a typo doesn't cost the link.
	if err := s.checkPassword(in.Password); err != nil {
		failLDAP(w, err)
		return
	}
	if _, ok := s.resets.Consume(in.Token); !ok {
		fail(w, http.StatusBadRequest, linkExpired)
		return
	}
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		// A suspended account that is only locked (not moved) can't get its password back this way either.
		if e, err := c.GetAttrs(dn, []string{"pwdAccountLockedTime"}); err == nil && len(e.Values("pwdAccountLockedTime")) > 0 {
			return errSuspended
		}
		return c.SetPassword(dn, in.Password)
	}) {
		return
	}
	s.sessions.RevokeFor(dn, "") // whoever was signed in as them (perhaps the reason for the reset) is signed out
	audit(nil, "password.reset", dn, "ip", clientIP(r))
	s.notifyPasswordChanged(dn, "You chose a new password with the link we emailed you.")
	writeJSON(w, map[string]string{"status": "ok"})
}

// notifyPasswordChanged emails the person that their password was changed, so a change they didn't make is noticed.
// It is best effort: no email address, mail turned off, or a mail failure never stops the change.
func (s *Server) notifyPasswordChanged(dn, how string) {
	cfg := s.c()
	if !cfg.Mail.Enabled {
		return
	}
	go func() {
		svc, err := dialSvc(cfg)
		if err != nil {
			return
		}
		defer svc.Close()
		e, err := svc.GetAttrs(dn, []string{"mail"})
		if err != nil || len(e.Values("mail")) == 0 {
			return
		}
		to := strings.TrimSpace(e.Values("mail")[0])
		err = s.send(cfg.Mail, mail.Message{To: to, Subject: "Your password was changed", Body: "The password for your account was changed on " +
			time.Now().Format("2 January 2006 at 15:04 MST") + ".\n\n" + how + "\n\n" +
			"If this was you, there is nothing more to do. If it wasn't, contact your administrator right away.\n"})
		if err != nil {
			log.Printf("password changed notice to %s failed: %v", to, err)
		}
	}()
}
