package api

import (
	"encoding/json"
	"net/http"

	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// getSettings reports the (read-only) connection setup, with a live check, and
// the editable field policy. Secrets are never included.
func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	conn := map[string]any{
		"url": s.cfg.LDAP.URL, "startTLS": s.cfg.LDAP.StartTLS, "bindDN": s.cfg.LDAP.BindDN,
		"usersBaseDN": s.cfg.Users.BaseDN, "groupsBaseDN": s.cfg.Groups.BaseDN,
		"suspendedBaseDN": s.cfg.Users.SuspendedBaseDN, "adminGroupDN": s.cfg.Admin.GroupDN,
	}
	c, err := s.svc()
	if err != nil {
		conn["ok"], conn["message"] = false, "Dapper can't connect or sign in with the service account. Check the server address and credentials."
	} else {
		defer c.Close()
		if _, _, err := c.ListPeople(s.cfg.Users.BaseDN, []string{"uid"}, 1); err != nil {
			conn["ok"], conn["message"] = false, "Connected, but the users container couldn't be read. Check users.baseDN."
		} else {
			conn["ok"], conn["message"] = true, "Connected."
		}
	}
	writeJSON(w, map[string]any{"connection": conn, "fields": s.policy.Fields()})
}

func (s *Server) putFieldPolicy(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		Fields []policy.Field `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if err := s.policy.Update(in.Fields); err != nil {
		fail(w, http.StatusBadRequest, "Couldn't save the settings: "+err.Error())
		return
	}
	audit(sess, "settings.fields.update", "policy")
	writeJSON(w, map[string]any{"fields": s.policy.Fields()})
}
