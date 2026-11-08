package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
	"github.com/koreesmith/dapper/backend/internal/users"
)

func (s *Server) groupsEnabled() bool { return s.c().Groups.BaseDN != "" }

// placeholderDN fills a group's member list when it would otherwise be empty:
// groupOfNames requires at least one member.
func (s *Server) placeholderDN() string { return "cn=dapper-placeholder," + s.c().Groups.BaseDN }

func (s *Server) isPlaceholder(dn string) bool { return strings.EqualFold(dn, s.placeholderDN()) }

func (s *Server) realMembers(ms []string) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if !s.isPlaceholder(m) {
			out = append(out, m)
		}
	}
	return out
}

func eqFold(a, b string) bool { return strings.EqualFold(a, b) }

// setMembers replaces a group's member list, keeping the placeholder when empty.
func (s *Server) setMembers(c *ldapclient.Client, groupDN string, members []string) error {
	if len(members) == 0 {
		members = []string{s.placeholderDN()}
	}
	return c.Modify(groupDN, []ldapclient.Change{{Op: "replace", Attr: s.c().Groups.MemberAttribute, Values: members}})
}

func (s *Server) memberships(c *ldapclient.Client, dn string) ([]ldapclient.Group, error) {
	if !s.groupsEnabled() {
		return nil, nil
	}
	return c.GroupsOfMember(s.c().Groups.BaseDN, s.c().Groups.ObjectClass, s.c().Groups.MemberAttribute, dn)
}

// replaceInGroups rewrites oldDN to newDN (or removes it when newDN is empty)
// in every group that lists it. Keeps groups consistent when accounts move or
// are deleted on servers without the refint overlay.
func (s *Server) replaceInGroups(c *ldapclient.Client, oldDN, newDN string) error {
	if !s.groupsEnabled() {
		return nil
	}
	gs, err := c.Groups(s.c().Groups.BaseDN, s.c().Groups.ObjectClass, s.c().Groups.MemberAttribute)
	if err != nil {
		return err
	}
	for _, g := range gs {
		var next []string
		changed := false
		for _, m := range g.Members {
			if eqFold(m, oldDN) {
				changed = true
				if newDN != "" {
					next = append(next, newDN)
				}
				continue
			}
			next = append(next, m)
		}
		if changed {
			if err := s.setMembers(c, g.DN, s.realMembers(next)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) groupDN(dn string) (string, error) {
	if !s.groupsEnabled() {
		return "", validationError("Group management isn't configured.")
	}
	if !underBase(s.c().Groups.BaseDN, dn) {
		return "", validationError("That group isn't managed by Dapper.")
	}
	return dn, nil
}

type groupSummary struct {
	DN      string `json:"dn"`
	Name    string `json:"name"`
	Members int    `json:"members"`
	Admin   bool   `json:"admin,omitempty"` // membership grants Dapper admin access
}

func (s *Server) listGroups(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	if !s.groupsEnabled() {
		writeJSON(w, []groupSummary{})
		return
	}
	var out []groupSummary
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		gs, err := c.Groups(s.c().Groups.BaseDN, s.c().Groups.ObjectClass, s.c().Groups.MemberAttribute)
		for _, g := range gs {
			out = append(out, groupSummary{DN: g.DN, Name: g.Name, Members: len(s.realMembers(g.Members)), Admin: eqFold(g.DN, s.c().Admin.GroupDN)})
		}
		return err
	}) {
		return
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	if out == nil {
		out = []groupSummary{}
	}
	writeJSON(w, out)
}

var groupNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if !s.groupsEnabled() {
		fail(w, http.StatusBadRequest, "Group management isn't configured.")
		return
	}
	var in struct{ Name, Description string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !groupNameRE.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "Group names may use letters, numbers, spaces, dots, dashes and underscores (up to 64 characters).")
		return
	}
	dn := "cn=" + in.Name + "," + s.c().Groups.BaseDN
	attrs := map[string][]string{
		"objectClass":                {s.c().Groups.ObjectClass},
		"cn":                         {in.Name},
		s.c().Groups.MemberAttribute: {s.placeholderDN()},
	}
	if d := strings.TrimSpace(in.Description); d != "" {
		attrs["description"] = []string{d}
	}
	if s.withSvc(w, func(c *ldapclient.Client) error { return c.Add(ldapclient.Entry{DN: dn, Attrs: attrs}) }) {
		audit(sess, "group.create", dn)
		writeJSON(w, map[string]string{"dn": dn, "name": in.Name})
	}
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	dn, err := s.groupDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	type member struct {
		DN       string `json:"dn"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	out := struct {
		DN      string   `json:"dn"`
		Name    string   `json:"name"`
		Members []member `json:"members"`
	}{DN: dn, Members: []member{}}
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		e, err := c.GetAttrs(dn, []string{"cn", s.c().Groups.MemberAttribute})
		if err != nil {
			return err
		}
		if v := e.Values("cn"); len(v) > 0 {
			out.Name = v[0]
		}
		for _, m := range s.realMembers(e.Values(s.c().Groups.MemberAttribute)) {
			mem := member{DN: m, Name: m}
			if pe, err := c.GetAttrs(m, users.Attrs); err == nil {
				u := users.FromEntry(*pe)
				mem.Username, mem.Name = u.Username, u.Name
			}
			out.Members = append(out.Members, mem)
		}
		return nil
	}) {
		return
	}
	sort.Slice(out.Members, func(i, j int) bool {
		return strings.ToLower(out.Members[i].Name) < strings.ToLower(out.Members[j].Name)
	})
	writeJSON(w, out)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.groupDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	if eqFold(dn, s.c().Admin.GroupDN) {
		fail(w, http.StatusBadRequest, "This is the administrators group that controls who can sign in as an admin, so it can't be deleted here.")
		return
	}
	if s.withSvc(w, func(c *ldapclient.Client) error { return c.Delete(dn) }) {
		audit(sess, "group.delete", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// changeMember adds or removes one account in a group.
func (s *Server) changeMember(add bool) handler {
	return func(w http.ResponseWriter, r *http.Request, sess *session.Session) {
		gdn, err := s.groupDN(r.URL.Query().Get("dn"))
		if err != nil {
			failLDAP(w, err)
			return
		}
		member := r.URL.Query().Get("member")
		if member == "" && r.Body != nil {
			var in struct{ Member string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			member = in.Member
		}
		if member, err = s.userDN(member); err != nil {
			failLDAP(w, err)
			return
		}
		if !add && eqFold(gdn, s.c().Admin.GroupDN) && eqFold(member, sess.DN) {
			fail(w, http.StatusBadRequest, "You can't remove yourself from the administrators group.")
			return
		}
		if !s.withSvc(w, func(c *ldapclient.Client) error {
			e, err := c.GetAttrs(gdn, []string{s.c().Groups.MemberAttribute})
			if err != nil {
				return err
			}
			cur := s.realMembers(e.Values(s.c().Groups.MemberAttribute))
			var next []string
			found := false
			for _, m := range cur {
				if eqFold(m, member) {
					found = true
					if !add {
						continue
					}
				}
				next = append(next, m)
			}
			if add && !found {
				next = append(next, member)
			}
			return s.setMembers(c, gdn, next)
		}) {
			return
		}
		act := "group.member.remove"
		if add {
			act = "group.member.add"
		}
		audit(sess, act, gdn, "member", member)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}
