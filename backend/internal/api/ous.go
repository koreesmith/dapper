package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Organizational units (OUs) are sections of the people container, such as departments or sites. Only
// the people container and the units inside it are managed here; the container itself and the suspended
// container can't be renamed or removed, and a unit can only be renamed or removed while it's empty
// (OpenLDAP can't rename a unit that has things in it, and removing one would orphan them).

var ouNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._&'-]{0,63}$`)

type ouInfo struct {
	DN       string `json:"dn"`
	Name     string `json:"name"`
	Parent   string `json:"parent"`
	Depth    int    `json:"depth"`
	People   int    `json:"people"`   // accounts directly in this unit
	Children int    `json:"children"` // units directly inside it
	Root     bool   `json:"root,omitempty"`
}

func dnDepth(base, dn string) int {
	if strings.EqualFold(base, dn) {
		return 0
	}
	n := strings.Count(strings.ToLower(dn), ",") - strings.Count(strings.ToLower(base), ",")
	if n < 0 {
		n = 0
	}
	return n
}

// inPeopleTree reports whether dn is the people container or inside it (but not the suspended container).
func (s *Server) inPeopleTree(dn string) bool {
	cfg := s.c()
	if eqFold(dn, cfg.Users.BaseDN) {
		return true
	}
	if cfg.Users.SuspendedBaseDN != "" && (eqFold(dn, cfg.Users.SuspendedBaseDN) || underBase(cfg.Users.SuspendedBaseDN, dn)) {
		return false
	}
	return underBase(cfg.Users.BaseDN, dn)
}

// listOUs returns the people container and every unit inside it, with how many people and units each holds directly.
func (s *Server) listOUs(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	cfg := s.c()
	var out []ouInfo
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		es, err := c.SearchAttrs(cfg.Users.BaseDN, "(objectClass=organizationalUnit)", []string{"ou"}, 0)
		if err != nil {
			return err
		}
		people, _, err := c.ListPeople(cfg.Users.BaseDN, []string{"uid"}, maxUsers)
		if err != nil {
			return err
		}
		count := map[string]int{}
		for _, p := range people {
			count[strings.ToLower(parentDN(p.DN))]++
		}
		kids := map[string]int{}
		units := []ouInfo{{DN: cfg.Users.BaseDN, Name: "Everyone (top level)", Root: true}}
		for _, e := range es {
			if eqFold(e.DN, cfg.Users.BaseDN) || !s.inPeopleTree(e.DN) {
				continue
			}
			units = append(units, ouInfo{DN: e.DN, Name: first(e.Values("ou")), Parent: parentDN(e.DN)})
			kids[strings.ToLower(parentDN(e.DN))]++
		}
		for _, u := range units {
			u.People, u.Children, u.Depth = count[strings.ToLower(u.DN)], kids[strings.ToLower(u.DN)], dnDepth(cfg.Users.BaseDN, u.DN)
			out = append(out, u)
		}
		// Parents before children, alphabetical within a level.
		sort.SliceStable(out, func(i, j int) bool {
			a, b := strings.ToLower(out[i].DN), strings.ToLower(out[j].DN)
			if strings.HasSuffix(a, b) && a != b {
				return false
			}
			if strings.HasSuffix(b, a) && a != b {
				return true
			}
			return out[i].Name < out[j].Name
		})
		return nil
	}) {
		return
	}
	writeJSON(w, map[string]any{"units": out, "baseDN": cfg.Users.BaseDN})
}

func parentDN(dn string) string {
	for i := 0; i < len(dn); i++ {
		if dn[i] == '\\' {
			i++
		} else if dn[i] == ',' {
			return dn[i+1:]
		}
	}
	return ""
}

func ouRDN(name string) string { return "ou=" + escapeRDN(name) }

func (s *Server) createOU(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ Parent, Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !ouNameRE.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "A unit's name can use letters, numbers, spaces and . _ & ' - (up to 64 characters).")
		return
	}
	if !s.inPeopleTree(in.Parent) {
		fail(w, http.StatusBadRequest, "Units can only be created inside the people container.")
		return
	}
	dn := ouRDN(in.Name) + "," + in.Parent
	if s.withSvc(w, func(c *ldapclient.Client) error {
		return c.Add(ldapclient.Entry{DN: dn, Attrs: map[string][]string{"objectClass": {"organizationalUnit"}, "ou": {in.Name}}})
	}) {
		audit(sess, "ou.create", dn)
		writeJSON(w, map[string]string{"dn": dn})
	}
}

// guardOU checks a unit may be renamed or removed: inside the people container, not the container itself, and empty.
func (s *Server) guardOU(c *ldapclient.Client, dn string) error {
	if eqFold(dn, s.c().Users.BaseDN) || !underBase(s.c().Users.BaseDN, dn) || !s.inPeopleTree(dn) {
		return validationError("That unit can't be changed here.")
	}
	es, err := c.SearchAttrs(dn, "(objectClass=*)", []string{"1.1"}, 2)
	if err != nil {
		return err
	}
	if n := len(es) - 1; n > 0 { // the unit itself comes back too
		return validationError("This unit still has " + strconv.Itoa(n) + " " + map[bool]string{true: "item", false: "items"}[n == 1] +
			" in it. Move or remove them first; a unit can only be renamed or removed while it's empty.")
	}
	return nil
}

func (s *Server) renameOU(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ DN, Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !ouNameRE.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "A unit's name can use letters, numbers, spaces and . _ & ' - (up to 64 characters).")
		return
	}
	newDN := ouRDN(in.Name) + "," + parentDN(in.DN)
	if s.withSvc(w, func(c *ldapclient.Client) error {
		if err := s.guardOU(c, in.DN); err != nil {
			return err
		}
		return c.Move(in.DN, ouRDN(in.Name), "")
	}) {
		audit(sess, "ou.rename", in.DN, "to", newDN)
		writeJSON(w, map[string]string{"dn": newDN})
	}
}

func (s *Server) deleteOU(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn := r.URL.Query().Get("dn")
	if s.withSvc(w, func(c *ldapclient.Client) error {
		if err := s.guardOU(c, dn); err != nil {
			return err
		}
		return c.Delete(dn)
	}) {
		audit(sess, "ou.delete", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// movePerson moves an account into another unit, keeping their group memberships and ending their sessions
// (their name in the directory changes).
func (s *Server) movePerson(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ DN, OU string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	dn, err := s.userDN(in.DN)
	if err != nil {
		failLDAP(w, err)
		return
	}
	if eqFold(dn, sess.DN) {
		fail(w, http.StatusBadRequest, "You can't move your own account. Ask another administrator.")
		return
	}
	if s.isSuspendedDN(dn) {
		fail(w, http.StatusBadRequest, "Restore this account before moving it to another unit.")
		return
	}
	if !s.inPeopleTree(in.OU) {
		fail(w, http.StatusBadRequest, "People can only be moved to the people container or a unit inside it.")
		return
	}
	if eqFold(parentDN(dn), in.OU) {
		fail(w, http.StatusBadRequest, "They're already there.")
		return
	}
	newDN := rdnOf(dn) + "," + in.OU
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		if ok, err := c.Exists(in.OU); err != nil {
			return err
		} else if !ok {
			return validationError("That unit no longer exists.")
		}
		if err := c.Move(dn, rdnOf(dn), in.OU); err != nil {
			return err
		}
		return s.replaceInGroups(c, dn, newDN)
	}) {
		return
	}
	s.sessions.RevokeFor(dn, "")
	audit(sess, "user.move", newDN, "from", dn)
	writeJSON(w, map[string]string{"dn": newDN})
}
