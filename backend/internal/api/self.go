package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
)

func (s *Server) getProfile(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	var resp map[string]any
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		e, err := c.GetAttrs(sess.DN, []string{"*"})
		if err != nil {
			return err
		}
		gs, err := s.memberships(c, sess.DN, first(e.Values("uid")), first(e.Values("gidNumber")))
		if err != nil {
			return err
		}
		groups := make([]string, 0, len(gs))
		for _, g := range gs {
			if g.Kind != kindPersonal { // everyone has one; it isn't interesting
				groups = append(groups, g.Name)
			}
		}
		name := first(e.Values("displayName"))
		if name == "" {
			name = first(e.Values("cn"))
		}
		resp = map[string]any{
			"dn": sess.DN, "username": first(e.Values("uid")), "name": name,
			"fields": s.fieldValues(e, func(f policy.Field) bool { return f.UserVisible }),
			"groups": groups,
		}
		return nil
	}) {
		return
	}
	writeJSON(w, resp)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		Attrs map[string][]string `json:"attrs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	var changed int
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		cur, err := c.GetAttrs(sess.DN, []string{"*"})
		if err != nil {
			return err
		}
		// Only fields the administrator has made editable; enforced here, not in the UI.
		changes, err := s.buildChanges(cur, in.Attrs, func(f policy.Field) bool { return f.UserEditable })
		if err != nil {
			return err
		}
		changed = len(changes)
		if changed == 0 {
			return nil
		}
		return c.Modify(sess.DN, changes)
	}) {
		return
	}
	if changed > 0 {
		audit(sess, "profile.update", sess.DN)
	}
	writeJSON(w, map[string]any{"status": "ok", "changed": changed})
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ Current, New string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	key := "pw:" + strings.ToLower(sess.DN)
	if s.limit.blocked(key) {
		fail(w, http.StatusTooManyRequests, "Too many incorrect attempts. Wait a few minutes and try again.")
		return
	}
	if in.New == in.Current {
		fail(w, http.StatusBadRequest, "Choose a new password that is different from the current one.")
		return
	}
	if err := s.checkPassword(in.New); err != nil {
		failLDAP(w, err)
		return
	}
	// Prove knowledge of the current password with a fresh bind as the user.
	probe, err := ldapclient.Dial(s.server())
	if err != nil {
		fail(w, http.StatusBadGateway, "The directory server is unavailable. Try again shortly.")
		return
	}
	err = probe.Bind(sess.DN, in.Current)
	if err != nil {
		probe.Close()
		s.limit.fail(key)
		fail(w, http.StatusBadRequest, "Your current password is incorrect.")
		return
	}
	s.limit.reset(key)
	// Change it as the person themselves, so the directory's own password rules (length, history, ...)
	// apply. They'd be skipped if the service account did it, because it's usually the directory manager.
	changeErr := probe.ChangePassword(sess.DN, in.Current, in.New)
	probe.Close()
	if changeErr != nil {
		if !ldapclient.IsCode(changeErr, ldapclient.CodeInsufficientAccess) {
			failLDAP(w, changeErr)
			return
		}
		// The directory doesn't let people change their own password directly: fall back to the
		// service account (Dapper's own minimum length still applies).
		if !s.withSvc(w, func(c *ldapclient.Client) error { return c.SetPassword(sess.DN, in.New) }) {
			return
		}
	}
	s.sessions.RevokeFor(sess.DN, sess.ID) // other devices must sign in again; this one carries on
	audit(sess, "password.change", sess.DN)
	writeJSON(w, map[string]string{"status": "ok"})
}
