package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
)

type passwordPolicy struct {
	MinLength      int    `json:"minLength"`             // the stricter of Dapper's minimum and the directory's
	Enforced       bool   `json:"enforced"`              // the directory has password rules of its own
	History        int    `json:"history,omitempty"`     // can't reuse the last N passwords
	MaxAgeDays     int    `json:"maxAgeDays,omitempty"`  // passwords expire after this many days
	Lockout        bool   `json:"lockout,omitempty"`     // an account locks after too many wrong passwords
	MaxFailures    int    `json:"maxFailures,omitempty"` //
	LockoutMinutes int    `json:"lockoutMinutes"`        // 0 = until an administrator unlocks it
	ExpiresOn      string `json:"expiresOn,omitempty"`   // when this person's password expires, if it does
	DaysLeft       *int   `json:"daysLeft,omitempty"`    // negative once expired
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// myPasswordPolicy tells the signed-in person the password rules that apply to them and when theirs expires.
func (s *Server) myPasswordPolicy(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	cfg := s.c()
	out := passwordPolicy{MinLength: cfg.Password.MinLength}
	if cfg.Password.PolicyDN != "" {
		if !s.withSvc(w, func(c *ldapclient.Client) error {
			me, err := c.GetAttrs(sess.DN, []string{"pwdPolicySubentry", "pwdChangedTime"})
			if err != nil {
				return err
			}
			dn := first(me.Values("pwdPolicySubentry")) // a policy set on this person wins
			if dn == "" {
				dn = cfg.Password.PolicyDN
			}
			pol, err := c.GetAttrs(dn, []string{"*"})
			if err != nil {
				if ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
					return nil // the policy entry is gone: only Dapper's own rule applies
				}
				return err
			}
			out.Enforced = true
			if n := atoi(first(pol.Values("pwdMinLength"))); n > out.MinLength {
				out.MinLength = n
			}
			out.History = atoi(first(pol.Values("pwdInHistory")))
			out.MaxAgeDays = atoi(first(pol.Values("pwdMaxAge"))) / 86400
			out.Lockout = first(pol.Values("pwdLockout")) == "TRUE"
			if out.Lockout {
				out.MaxFailures = atoi(first(pol.Values("pwdMaxFailure")))
				out.LockoutMinutes = atoi(first(pol.Values("pwdLockoutDuration"))) / 60
			}
			if age := atoi(first(pol.Values("pwdMaxAge"))); age > 0 {
				if t, err := time.Parse("20060102150405Z", first(me.Values("pwdChangedTime"))); err == nil {
					exp := t.Add(time.Duration(age) * time.Second)
					out.ExpiresOn = exp.UTC().Format(time.RFC3339)
					d := int(time.Until(exp).Hours() / 24)
					if time.Until(exp) < 0 {
						d = -int(-time.Until(exp).Hours()/24) - 1
					}
					out.DaysLeft = &d
				}
			}
			return nil
		}) {
			return
		}
	}
	writeJSON(w, out)
}
