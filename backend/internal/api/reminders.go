package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/mail"
	"github.com/koreesmith/dapper/backend/internal/reminders"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Password expiry reminders. When the directory's password rules include a maximum age, Dapper emails people 14, 7 and
// 1 days before their password expires. A check runs a minute after start and then every six hours; what was already
// sent is remembered (in the data directory), so nobody gets the same reminder twice.

// reminderRun is what the last check did, shown on the Email page.
type reminderRun struct {
	mu      sync.Mutex
	state   *reminders.State
	lastRun time.Time
	sent    int
	err     string
	busy    bool
}

func (r *reminderRun) store(dataDir string) *reminders.State {
	if r.state == nil {
		r.state = reminders.Open(dataDir)
	}
	return r.state
}

func (s *Server) reminderStatus() map[string]any {
	s.remind.mu.Lock()
	defer s.remind.mu.Unlock()
	out := map[string]any{"rulesOn": s.c().Password.PolicyDN != "", "days": reminders.Days}
	if !s.remind.lastRun.IsZero() {
		out["lastRun"] = s.remind.lastRun.UTC().Format(time.RFC3339)
		out["lastSent"] = s.remind.sent
		out["lastError"] = s.remind.err
	}
	return out
}

func (s *Server) reminderLoop() {
	time.Sleep(time.Minute)
	for {
		if s.c().Mail.Enabled && !s.c().Mail.NoReminders {
			if _, err := s.checkExpiry(time.Now()); err != nil {
				log.Printf("password expiry reminders: %v", err)
			}
		}
		time.Sleep(6 * time.Hour)
	}
}

// remindersRunNow lets an administrator run the check immediately instead of waiting for the next one.
func (s *Server) remindersRunNow(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	if !s.resetEnabled() {
		fail(w, http.StatusBadRequest, "Turn on email first.")
		return
	}
	if s.c().Password.PolicyDN == "" {
		fail(w, http.StatusBadRequest, "Password rules aren't turned on, so passwords don't expire and there is nothing to remind anyone about. Turn them on from the Server page.")
		return
	}
	n, err := s.checkExpiry(time.Now())
	if err != nil {
		fail(w, http.StatusBadGateway, "The check couldn't finish: "+err.Error()+".")
		return
	}
	audit(sess, "mail.reminders.run", "settings", "sent", fmt.Sprint(n))
	writeJSON(w, map[string]any{"sent": n})
}

// checkExpiry sends every reminder that is due now and returns how many it sent.
func (s *Server) checkExpiry(now time.Time) (int, error) {
	s.remind.mu.Lock()
	if s.remind.busy {
		s.remind.mu.Unlock()
		return 0, fmt.Errorf("a check is already running")
	}
	s.remind.busy = true
	st := s.remind.store(s.c().DataDir)
	s.remind.mu.Unlock()

	sent, err := s.sendDueReminders(now, st)

	s.remind.mu.Lock()
	s.remind.busy = false
	s.remind.lastRun, s.remind.sent, s.remind.err = now, sent, ""
	if err != nil {
		s.remind.err = err.Error()
	}
	s.remind.mu.Unlock()
	return sent, err
}

func (s *Server) sendDueReminders(now time.Time, st *reminders.State) (int, error) {
	cfg := s.c()
	if cfg.Password.PolicyDN == "" || !cfg.Mail.Enabled || cfg.Mail.PublicURL == "" {
		return 0, nil // no password rules means passwords never expire
	}
	svc, err := dialSvc(cfg)
	if err != nil {
		return 0, fmt.Errorf("couldn't reach the directory")
	}
	defer svc.Close()

	maxAge := map[string]int{} // policy entry -> seconds, 0 = never expires
	ageOf := func(dn string) int {
		if v, ok := maxAge[dn]; ok {
			return v
		}
		n := 0
		if pol, err := svc.GetAttrs(dn, []string{"pwdMaxAge"}); err == nil {
			n = atoi(first(pol.Values("pwdMaxAge")))
		}
		maxAge[dn] = n
		return n
	}
	people, err := svc.SearchComplete(cfg.Users.BaseDN, "(&(objectClass=person)(mail=*)(pwdChangedTime=*))",
		[]string{"mail", "uid", "givenName", "pwdChangedTime", "pwdPolicySubentry", "pwdAccountLockedTime"})
	if err != nil {
		return 0, fmt.Errorf("couldn't list people")
	}
	sent := 0
	for _, e := range people {
		if len(e.Values("pwdAccountLockedTime")) > 0 || s.isSuspendedDN(e.DN) {
			continue
		}
		pol := first(e.Values("pwdPolicySubentry")) // a policy set on this person wins
		if pol == "" {
			pol = cfg.Password.PolicyDN
		}
		age := ageOf(pol)
		changed, perr := time.Parse("20060102150405Z", first(e.Values("pwdChangedTime")))
		if age <= 0 || perr != nil {
			continue
		}
		expiry := changed.Add(time.Duration(age) * time.Second)
		level, due := reminders.Due(now, expiry, st.Sent(e.DN, expiry))
		if !due {
			continue
		}
		if s.sendExpiryEmail(e, expiry, reminders.DaysLeft(now, expiry)) {
			if err := st.Mark(e.DN, expiry, level); err != nil {
				log.Printf("password expiry reminders: couldn't remember what was sent: %v", err)
			}
			sent++
			audit(nil, "reminder.sent", e.DN, "days", fmt.Sprint(reminders.DaysLeft(now, expiry)))
		}
	}
	st.Forget(now)
	return sent, nil
}

// sendExpiryEmail tells one person when their password expires. It reports whether the mail server took it.
func (s *Server) sendExpiryEmail(e ldapclient.Entry, expiry time.Time, days int) bool {
	cfg := s.c()
	to := strings.TrimSpace(first(e.Values("mail")))
	if to == "" {
		return false
	}
	when := "in " + fmt.Sprint(days) + " days"
	if days == 1 {
		when = "tomorrow"
	}
	site, host := siteName(cfg.Mail), hostOf(cfg.Mail.PublicURL)
	err := s.send(cfg.Mail, mail.Message{To: to, Subject: "Your " + site + " password expires " + when,
		Body: greeting(&e) +
			"The password for the account \"" + accountName(&e, e.DN) + "\" on " + host + " expires " + when +
			", on " + expiry.Local().Format("2 January 2006") + ".\n\n" +
			"To choose a new one, sign in at " + cfg.Mail.PublicURL + " and use \"Change password\" on the My account page.\n\n" +
			"If it expires first, use \"Forgot your password?\" on the sign-in page, or ask an administrator to reset it.\n\n" + site + "\n"})
	if err != nil {
		log.Printf("password expiry reminder to %s failed: %v", to, err)
		return false
	}
	return true
}
