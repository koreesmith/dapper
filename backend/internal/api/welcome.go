package api

import (
	"strings"
	"time"

	"github.com/koreesmith/dapper/backend/internal/mail"
)

// welcomeTTL is how long a new person's set-password link works. It has to outlast a weekend.
const welcomeTTL = 7 * 24 * time.Hour

// sendWelcome emails a new person their username and a one-time link to choose their own password. No password is
// ever put in an email. It reports whether it was sent and, in plain words, why not; the account exists either way.
func (s *Server) sendWelcome(dn, username string, attrs map[string][]string) (bool, string) {
	cfg := s.c()
	if !s.resetEnabled() {
		return false, "Email isn't set up, so no welcome message was sent."
	}
	to := ""
	if v := attrs["mail"]; len(v) > 0 {
		to = strings.TrimSpace(v[0])
	}
	if to == "" {
		return false, "They have no email address, so no welcome message was sent."
	}
	tok, err := s.resets.IssueFor(dn, welcomeTTL)
	if err != nil {
		return false, "Couldn't prepare the welcome message."
	}
	given := ""
	if v := attrs["givenName"]; len(v) > 0 {
		given = strings.TrimSpace(v[0])
	}
	hello := "Hello,\n\n"
	if given != "" && !strings.ContainsAny(given, "\r\n") {
		hello = "Hello " + given + ",\n\n"
	}
	site, host := siteName(cfg.Mail), hostOf(cfg.Mail.PublicURL)
	err = s.send(cfg.Mail, mail.Message{To: to, Subject: "Welcome to " + site, Body: hello +
		"An account has been created for you on " + host + ".\n\n" +
		"Your username is: " + username + "\n\n" +
		"To choose your password, open this link within the next 7 days. It can be used once:\n\n" +
		cfg.Mail.PublicURL + "/#/reset/" + tok + "\n\n" +
		"After that you can sign in at " + cfg.Mail.PublicURL + "\n\n" + site + "\n"})
	if err != nil {
		s.resets.Revoke(dn)
		return false, "The welcome message couldn't be sent: " + err.Error() + "."
	}
	return true, "A welcome message with a link to choose a password was sent to " + to + "."
}
