package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
	"github.com/koreesmith/dapper/backend/internal/users"
)

// Changing a group's number is delicate: people point at it through their own gidNumber, and
// files on servers are owned by the number, not the name. Every change here is previewed, needs
// approval, and refuses a number another group already uses.

type gidPerson struct {
	DN       string `json:"dn"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

func toGIDPeople(es []ldapclient.Entry) []gidPerson {
	out := make([]gidPerson, 0, len(es))
	for _, e := range es {
		u := users.FromEntry(e)
		out = append(out, gidPerson{DN: u.DN, Username: u.Username, Name: u.Name})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func parseGID(raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 2147483647 {
		return 0, validationError("The group ID must be a whole number from 1 upward.")
	}
	return n, nil
}

// peopleWithGID finds accounts whose primary group number is gid.
func (s *Server) peopleWithGID(c *ldapclient.Client, gid string) ([]ldapclient.Entry, error) {
	es, err := c.SearchAttrs(s.c().Users.BaseDN, "(gidNumber="+ldapEscape(gid)+")", users.Attrs, 0)
	if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
		return nil, err
	}
	return es, nil
}

// otherGroupWithGID returns another group (not dn) that already uses gid, if any.
func (s *Server) otherGroupWithGID(c *ldapclient.Client, dn, gid string) (*ldapclient.GroupEntry, error) {
	gs, err := c.AllGroups(s.c().Groups.BaseDN)
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if g.GID == gid && !eqFold(g.DN, dn) {
			return &g, nil
		}
	}
	return nil, nil
}

type gidChange struct {
	DN         string `json:"dn"`
	GID        string `json:"gid"`
	MovePeople bool   `json:"movePeople"`
	Confirmed  bool   `json:"confirmed"`
}

type gidPreview struct {
	Group     string      `json:"group"`
	Old       string      `json:"old"`
	New       string      `json:"new"`
	Moving    []gidPerson `json:"moving"`  // people whose primary group ID would change with it
	Adopted   []gidPerson `json:"adopted"` // people who already have the new number and become its primary members
	Conflict  string      `json:"conflict,omitempty"`
	Warnings  []string    `json:"warnings"`
	ChgrpHint string      `json:"chgrpHint,omitempty"`
}

// planGroupGID works out what changing a Linux group's number would do, without changing anything.
func (s *Server) planGroupGID(c *ldapclient.Client, req gidChange) (*gidPreview, *ldapclient.GroupEntry, error) {
	g, err := c.GetGroup(req.DN)
	if err != nil {
		return nil, nil, err
	}
	if g.GID == "" {
		return nil, nil, validationError("This group has no group ID because it isn't a Linux group, so there's nothing to change.")
	}
	n, err := parseGID(req.GID)
	if err != nil {
		return nil, nil, err
	}
	newGID := strconv.Itoa(n)
	p := &gidPreview{Group: g.Name, Old: g.GID, New: newGID, Moving: []gidPerson{}, Adopted: []gidPerson{}, Warnings: []string{}}
	if newGID == g.GID {
		return nil, nil, validationError("That is already this group's ID.")
	}
	if other, err := s.otherGroupWithGID(c, g.DN, newGID); err != nil {
		return nil, nil, err
	} else if other != nil {
		p.Conflict = "The group “" + other.Name + "” already has the ID " + newGID + ". Two groups can't share an ID."
	}
	if es, err := s.peopleWithGID(c, g.GID); err != nil {
		return nil, nil, err
	} else {
		p.Moving = toGIDPeople(es)
	}
	if es, err := s.peopleWithGID(c, newGID); err != nil {
		return nil, nil, err
	} else {
		p.Adopted = toGIDPeople(es)
	}
	if n < 1000 {
		p.Warnings = append(p.Warnings, "IDs below 1000 are normally reserved for the system's own groups.")
	}
	if len(p.Moving) > 0 && !req.MovePeople {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d %s currently use %s as their primary group. If you don't move them with it, they'll be left pointing at the old number %s, which then belongs to no group.",
			len(p.Moving), map[bool]string{true: "person", false: "people"}[len(p.Moving) == 1], g.Name, g.GID))
	}
	if len(p.Adopted) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%d %s already use %s as their primary group ID; they will belong to %s once it has that number.",
			len(p.Adopted), map[bool]string{true: "person", false: "people"}[len(p.Adopted) == 1], newGID, g.Name))
	}
	p.Warnings = append(p.Warnings, "This changes the number in the directory only. Files on your servers that belong to the old number are not changed.")
	p.ChgrpHint = fmt.Sprintf("sudo find / -xdev -gid %s -exec chgrp -h %s {} +", g.GID, newGID)
	return p, g, nil
}

func (s *Server) groupGID(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req gidChange
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if _, err := s.groupDN(req.DN); err != nil {
		failLDAP(w, err)
		return
	}
	var prev *gidPreview
	moved := 0
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		unixMu.Lock() // no new numbers are handed out while this is checked and applied
		defer unixMu.Unlock()
		p, g, err := s.planGroupGID(c, req)
		if err != nil {
			return err
		}
		prev = p
		if !req.Confirmed {
			return nil
		}
		if p.Conflict != "" {
			return validationError(p.Conflict)
		}
		if err := c.Modify(g.DN, []ldapclient.Change{{Op: "replace", Attr: "gidNumber", Values: []string{p.New}}}); err != nil {
			return err
		}
		if req.MovePeople {
			for _, person := range p.Moving {
				if err := c.Modify(person.DN, []ldapclient.Change{{Op: "replace", Attr: "gidNumber", Values: []string{p.New}}}); err != nil {
					return fmt.Errorf("the group's ID was changed, but updating %s failed (%d of %d people were moved): %w", person.Username, moved, len(p.Moving), err)
				}
				moved++
			}
		}
		return nil
	}) {
		return
	}
	if req.Confirmed {
		audit(sess, "group.gid.change", req.DN, "from", prev.Old, "to", prev.New, "peopleMoved", strconv.Itoa(moved))
		writeJSON(w, map[string]any{"ok": true, "moved": moved, "preview": prev})
		return
	}
	writeJSON(w, prev)
}

// ---- changing a person's primary group number ----

func (s *Server) personGID(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req struct {
		DN        string `json:"dn"`
		GID       string `json:"gid"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	dn, err := s.userDN(req.DN)
	if err != nil {
		failLDAP(w, err)
		return
	}
	n, err := parseGID(req.GID)
	if err != nil {
		failLDAP(w, err)
		return
	}
	newGID := strconv.Itoa(n)
	var out map[string]any
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		e, err := c.GetAttrs(dn, []string{"objectClass", "gidNumber", "uid"})
		if err != nil {
			return err
		}
		if !hasObjectClass(e, "posixAccount") {
			return validationError("This account isn't a Linux account, so it has no primary group.")
		}
		old := first(e.Values("gidNumber"))
		if old == newGID {
			return validationError("That is already their primary group ID.")
		}
		gs, err := c.AllGroups(s.c().Groups.BaseDN)
		if err != nil {
			return err
		}
		group := ""
		for _, g := range gs {
			if g.GID == newGID {
				group = g.Name
			}
		}
		if group == "" {
			return validationError("No group has the ID " + newGID + ", so it can't be their primary group. Create or renumber a group first.")
		}
		out = map[string]any{"person": first(e.Values("uid")), "old": old, "new": newGID, "group": group,
			"warnings": []string{"This changes the number in the directory only. Files on your servers owned by the old group number are not changed."}}
		if !req.Confirmed {
			return nil
		}
		return c.Modify(dn, []ldapclient.Change{{Op: "replace", Attr: "gidNumber", Values: []string{newGID}}})
	}) {
		return
	}
	if req.Confirmed {
		audit(sess, "user.gid.change", dn, "to", newGID)
		out["ok"] = true
	}
	writeJSON(w, out)
}

// ---- finding mismatches ----

type gidFix struct {
	Label     string `json:"label"`
	Kind      string `json:"kind"` // groupGID, personGID, createGroup
	GroupDN   string `json:"groupDN,omitempty"`
	PersonDN  string `json:"personDN,omitempty"`
	GID       string `json:"gid"`
	GroupName string `json:"groupName,omitempty"`
}

type gidIssue struct {
	Kind   string   `json:"kind"`
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Fixes  []gidFix `json:"fixes"`
}

// checkGroups looks for group-number mismatches: personal groups whose number differs from
// the person's own, people whose primary group number matches no group, and groups sharing a number.
func (s *Server) checkGroups(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	issues := []gidIssue{}
	if !s.groupsEnabled() {
		writeJSON(w, map[string]any{"issues": issues})
		return
	}
	cfg := s.c()
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		gs, err := c.AllGroups(cfg.Groups.BaseDN)
		if err != nil {
			return err
		}
		ppl, err := c.SearchAttrs(cfg.Users.BaseDN, "(objectClass=posixAccount)", append(append([]string(nil), users.Attrs...), "gidNumber", "uidNumber"), 0)
		if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			return err
		}
		byGID := map[string][]ldapclient.GroupEntry{}
		byName := map[string]ldapclient.GroupEntry{}
		for _, g := range gs {
			if g.GID != "" {
				byGID[g.GID] = append(byGID[g.GID], g)
				if !g.IsDNGroup() {
					byName[strings.ToLower(g.Name)] = g
				}
			}
		}
		// 1. Two or more groups sharing a number.
		var dupGIDs []string
		for gid, list := range byGID {
			if len(list) > 1 {
				dupGIDs = append(dupGIDs, gid)
			}
		}
		sort.Strings(dupGIDs)
		for _, gid := range dupGIDs {
			var names []string
			for _, g := range byGID[gid] {
				names = append(names, g.Name)
			}
			issues = append(issues, gidIssue{Kind: "duplicate", Title: "Several groups share the number " + gid,
				Detail: strings.Join(names, ", ") + ". Open one of them and give it its own number.", Fixes: []gidFix{}})
		}
		// 2 and 3: compare each person with their groups.
		sort.Slice(ppl, func(i, j int) bool { return ppl[i].DN < ppl[j].DN })
		for _, e := range ppl {
			u := users.FromEntry(e)
			gid := first(e.Values("gidNumber"))
			if gid == "" {
				continue
			}
			personalGroup, hasPersonal := byName[strings.ToLower(u.Username)]
			if hasPersonal && personalGroup.GID != gid && !personalGroup.IsDNGroup() {
				var fixes []gidFix
				if len(byGID[gid]) == 0 { // the person's number is free: the group can take it
					fixes = append(fixes, gidFix{Label: "Give the group “" + personalGroup.Name + "” the ID " + gid, Kind: "groupGID", GroupDN: personalGroup.DN, GID: gid, GroupName: personalGroup.Name})
				}
				fixes = append(fixes, gidFix{Label: "Make " + u.Username + "'s primary group ID " + personalGroup.GID + " (the group's number)", Kind: "personGID", PersonDN: u.DN, GID: personalGroup.GID, GroupName: personalGroup.Name})
				issues = append(issues, gidIssue{Kind: "personal-mismatch",
					Title:  "The group “" + personalGroup.Name + "” has a different number from " + u.Username,
					Detail: fmt.Sprintf("%s's account says their primary group is %s, but the group named %s has the ID %s.", u.Username, gid, personalGroup.Name, personalGroup.GID),
					Fixes:  fixes})
				continue
			}
			if len(byGID[gid]) == 0 {
				fixes := []gidFix{}
				if !hasPersonal {
					fixes = append(fixes, gidFix{Label: "Create a group “" + u.Username + "” with the ID " + gid, Kind: "createGroup", GID: gid, GroupName: u.Username})
				}
				issues = append(issues, gidIssue{Kind: "no-group", Title: u.Username + " has a primary group number with no group",
					Detail: fmt.Sprintf("%s's account says their primary group ID is %s, but no group has that number.", u.Username, gid), Fixes: fixes})
			}
		}
		return nil
	}) {
		return
	}
	writeJSON(w, map[string]any{"issues": issues})
}
