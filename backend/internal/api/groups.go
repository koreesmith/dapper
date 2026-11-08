package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/config"
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

func (s *Server) groupDN(dn string) (string, error) {
	if !s.groupsEnabled() {
		return "", validationError("Group management isn't configured.")
	}
	if !underBase(s.c().Groups.BaseDN, dn) {
		return "", validationError("That group isn't managed by Dapper.")
	}
	return dn, nil
}

// Group kinds shown to people.
const (
	kindShared   = "shared"   // members listed by full name (groupOfNames and similar)
	kindLinux    = "linux"    // a Linux group (posixGroup) with members listed by username
	kindPersonal = "personal" // a Linux group named after one person, which is their primary group
)

// personIDs maps lower-cased usernames to their numeric group id, from the people container.
func (s *Server) personGIDs(c *ldapclient.Client) (map[string]string, error) {
	cfg := s.c()
	es, err := c.SearchAttrs(cfg.Users.BaseDN, "(objectClass=posixAccount)", []string{"uid", "gidNumber"}, 0)
	if err != nil {
		if ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(es))
	for _, e := range es {
		out[strings.ToLower(first(e.Values("uid")))] = first(e.Values("gidNumber"))
	}
	return out, nil
}

func kindOf(g ldapclient.GroupEntry, gids map[string]string) string {
	if g.IsDNGroup() {
		return kindShared
	}
	if gid, ok := gids[strings.ToLower(g.Name)]; ok && gid != "" && gid == g.GID {
		return kindPersonal
	}
	return kindLinux
}

// ---- memberships (used by the person and My account pages) ----

type membership struct {
	DN      string `json:"dn"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Primary bool   `json:"primary,omitempty"`
}

func (s *Server) memberships(c *ldapclient.Client, personDN, username, gid string) ([]membership, error) {
	if !s.groupsEnabled() {
		return nil, nil
	}
	gids := map[string]string{username: gid}
	member, primary, err := c.GroupsFor(s.c().Groups.BaseDN, personDN, username, gid)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []membership
	for _, g := range member {
		seen[strings.ToLower(g.DN)] = true
		out = append(out, membership{DN: g.DN, Name: g.Name, Kind: kindOf(g, map[string]string{strings.ToLower(username): gid})})
	}
	for _, g := range primary {
		if seen[strings.ToLower(g.DN)] {
			for i := range out {
				if eqFold(out[i].DN, g.DN) {
					out[i].Primary = true
				}
			}
			continue
		}
		out = append(out, membership{DN: g.DN, Name: g.Name, Kind: kindOf(g, gids), Primary: true})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Primary != out[j].Primary {
			return out[i].Primary
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ---- keeping groups consistent when an account moves or is deleted ----

// setMembers replaces a DN-style group's member list, keeping the placeholder when empty.
func (s *Server) setMembers(c *ldapclient.Client, groupDN, attr string, members []string) error {
	if len(members) == 0 {
		members = []string{s.placeholderDN()}
	}
	return c.Modify(groupDN, []ldapclient.Change{{Op: "replace", Attr: attr, Values: members}})
}

// replaceInGroups rewrites oldDN to newDN (or removes it when newDN is empty) in every
// group that lists it by full name. Keeps groups consistent when accounts move or are
// deleted on servers without the refint overlay.
func (s *Server) replaceInGroups(c *ldapclient.Client, oldDN, newDN string) error {
	if !s.groupsEnabled() {
		return nil
	}
	gs, err := c.AllGroups(s.c().Groups.BaseDN)
	if err != nil {
		return err
	}
	for _, g := range gs {
		if !g.IsDNGroup() {
			continue
		}
		attr := g.DNMemberAttr(s.c().Groups.MemberAttribute)
		var cur []string
		for _, m := range g.Members {
			cur = append(cur, m)
		}
		var next []string
		changed := false
		for _, m := range cur {
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
			if err := s.setMembers(c, g.DN, attr, s.realMembers(next)); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeUIDFromGroups takes a username out of every Linux group's member list.
func (s *Server) removeUIDFromGroups(c *ldapclient.Client, uid string) error {
	if !s.groupsEnabled() || uid == "" {
		return nil
	}
	es, err := c.SearchAttrs(s.c().Groups.BaseDN, "(&(objectClass=posixGroup)(memberUid="+ldapEscape(uid)+"))", []string{"cn"}, 0)
	if err != nil {
		return err
	}
	for _, e := range es {
		if err := c.Modify(e.DN, []ldapclient.Change{{Op: "delete", Attr: "memberUid", Values: []string{uid}}}); err != nil &&
			!ldapclient.IsCode(err, ldapclient.CodeNoSuchAttribute) {
			return err
		}
	}
	return nil
}

// ---- adding and removing a member, for either kind of group ----

// personUID returns the username used in Linux groups' memberUid for a person.
func personUID(c *ldapclient.Client, personDN string) (string, error) {
	e, err := c.GetAttrs(personDN, []string{"uid"})
	if err != nil {
		return "", err
	}
	if v := first(e.Values("uid")); v != "" {
		return v, nil
	}
	_, v := rdnParts(personDN)
	return v, nil
}

// addMember puts a person in a group, whichever kind it is. Adding someone who is already a member is not an error.
func (s *Server) addMember(c *ldapclient.Client, groupDN, personDN string) error {
	g, err := c.GetGroup(groupDN)
	if err != nil {
		return err
	}
	if g.IsDNGroup() {
		attr := g.DNMemberAttr(s.c().Groups.MemberAttribute)
		cur := s.realMembers(g.Members)
		for _, m := range cur {
			if eqFold(m, personDN) {
				return nil
			}
		}
		return s.setMembers(c, groupDN, attr, append(cur, personDN))
	}
	uid, err := personUID(c, personDN)
	if err != nil {
		return err
	}
	err = c.Modify(groupDN, []ldapclient.Change{{Op: "add", Attr: "memberUid", Values: []string{uid}}})
	if ldapclient.IsCode(err, ldapclient.CodeAttributeOrValueExist) {
		return nil
	}
	return err
}

func (s *Server) removeMember(c *ldapclient.Client, groupDN, personDN string) error {
	g, err := c.GetGroup(groupDN)
	if err != nil {
		return err
	}
	if g.IsDNGroup() {
		attr := g.DNMemberAttr(s.c().Groups.MemberAttribute)
		var next []string
		for _, m := range s.realMembers(g.Members) {
			if !eqFold(m, personDN) {
				next = append(next, m)
			}
		}
		return s.setMembers(c, groupDN, attr, next)
	}
	uid, err := personUID(c, personDN)
	if err != nil {
		return err
	}
	err = c.Modify(groupDN, []ldapclient.Change{{Op: "delete", Attr: "memberUid", Values: []string{uid}}})
	if ldapclient.IsCode(err, ldapclient.CodeNoSuchAttribute) {
		return nil
	}
	return err
}

// ---- HTTP: listing ----

type groupSummary struct {
	DN      string `json:"dn"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	GID     string `json:"gid,omitempty"`
	Members int    `json:"members"`
	Admin   bool   `json:"admin,omitempty"` // membership grants Dapper admin access
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	empty := map[string]any{"groups": []groupSummary{}, "total": 0, "page": 1, "pageSize": 25, "counts": map[string]int{}}
	if !s.groupsEnabled() {
		writeJSON(w, empty)
		return
	}
	var all []groupSummary
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		gids, err := s.personGIDs(c)
		if err != nil {
			return err
		}
		gs, err := c.AllGroups(s.c().Groups.BaseDN)
		if err != nil {
			return err
		}
		for _, g := range gs {
			sum := groupSummary{DN: g.DN, Name: g.Name, Kind: kindOf(g, gids), GID: g.GID, Admin: eqFold(g.DN, s.c().Admin.GroupDN)}
			sum.Members = len(s.realMembers(g.Members)) + len(g.MemberUIDs)
			all = append(all, sum)
		}
		return nil
	}) {
		return
	}

	counts := map[string]int{}
	for _, g := range all {
		counts[g.Kind]++
	}
	q := r.URL.Query()
	kind, search := q.Get("kind"), strings.ToLower(strings.TrimSpace(q.Get("q")))
	var out []groupSummary
	for _, g := range all {
		if kind != "" && kind != "all" && g.Kind != kind {
			continue
		}
		if kind == "" && g.Kind == kindPersonal && search == "" {
			// default view: shared and Linux groups first; personal ones appear when asked for or searched
		}
		if search != "" && !strings.Contains(strings.ToLower(g.Name), search) && !strings.Contains(g.GID, search) {
			continue
		}
		out = append(out, g)
	}
	rank := map[string]int{kindShared: 0, kindLinux: 1, kindPersonal: 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size < 1 || size > 500 {
		size = 25
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * size
	if start > len(out) {
		start = len(out)
	}
	end := start + size
	if end > len(out) {
		end = len(out)
	}
	if out == nil {
		out = []groupSummary{}
	}
	writeJSON(w, map[string]any{"groups": out[start:end], "total": len(out), "page": page, "pageSize": size, "counts": counts})
}

var groupNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if !s.groupsEnabled() {
		fail(w, http.StatusBadRequest, "Group management isn't configured.")
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Kind        string `json:"kind"` // standard (default) or linux
		GID         string `json:"gid"`
		// Adopt: the number is already some people's primary group, and this new group is the one
		// they were missing. Only another group using the number is a clash.
		Adopt bool `json:"adopt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !groupNameRE.MatchString(in.Name) {
		fail(w, http.StatusBadRequest, "Group names may use letters, numbers, spaces, dots, dashes and underscores (up to 64 characters).")
		return
	}
	cfg := s.c()
	dn := "cn=" + escapeRDN(in.Name) + "," + cfg.Groups.BaseDN
	attrs := map[string][]string{"objectClass": {cfg.Groups.ObjectClass}, "cn": {in.Name}, cfg.Groups.MemberAttribute: {s.placeholderDN()}}
	linux := in.Kind == "linux"
	if linux {
		if strings.ContainsAny(in.Name, " ") {
			fail(w, http.StatusBadRequest, "Linux group names can't contain spaces.")
			return
		}
		attrs = map[string][]string{"objectClass": {"posixGroup"}, "cn": {in.Name}}
	}
	if d := strings.TrimSpace(in.Description); d != "" {
		attrs["description"] = []string{d}
	}
	var gid string
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		if linux {
			unixMu.Lock() // keep "pick a free number" and "use it" together
			defer unixMu.Unlock()
			var id int
			var err error
			if in.Adopt && strings.TrimSpace(in.GID) != "" {
				if id, err = parseGID(in.GID); err == nil {
					var other *ldapclient.GroupEntry
					if other, err = s.otherGroupWithGID(c, "", strconv.Itoa(id)); err == nil && other != nil {
						err = validationError("The group “" + other.Name + "” already has the ID " + strconv.Itoa(id) + ".")
					}
				}
			} else {
				id, err = s.chooseID(c, cfg, in.GID)
			}
			if err != nil {
				return err
			}
			gid = strconv.Itoa(id)
			attrs["gidNumber"] = []string{gid}
		}
		return c.Add(ldapclient.Entry{DN: dn, Attrs: attrs})
	}) {
		return
	}
	audit(sess, "group.create", dn, "kind", map[bool]string{true: "linux", false: "standard"}[linux])
	writeJSON(w, map[string]string{"dn": dn, "name": in.Name, "gid": gid})
}

// ---- HTTP: one group ----

type groupMember struct {
	DN       string `json:"dn"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// resolveUIDs looks up people by username, returning those found and keeping unknown names.
func (s *Server) resolveUIDs(c *ldapclient.Client, uids []string) []groupMember {
	found := map[string]groupMember{}
	for start := 0; start < len(uids); start += 40 {
		end := start + 40
		if end > len(uids) {
			end = len(uids)
		}
		var terms strings.Builder
		for _, u := range uids[start:end] {
			terms.WriteString("(uid=" + ldapEscape(u) + ")")
		}
		es, err := c.SearchAttrs(s.c().Users.BaseDN, "(|"+terms.String()+")", users.Attrs, 0)
		if err != nil {
			continue
		}
		for _, e := range es {
			u := users.FromEntry(e)
			found[strings.ToLower(u.Username)] = groupMember{DN: u.DN, Username: u.Username, Name: u.Name}
		}
	}
	out := make([]groupMember, 0, len(uids))
	for _, u := range uids {
		if m, ok := found[strings.ToLower(u)]; ok {
			out = append(out, m)
		} else {
			out = append(out, groupMember{Username: u, Name: u})
		}
	}
	return out
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	dn, err := s.groupDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	out := struct {
		DN             string        `json:"dn"`
		Name           string        `json:"name"`
		Kind           string        `json:"kind"`
		GID            string        `json:"gid,omitempty"`
		Members        []groupMember `json:"members"`
		PrimaryMembers []groupMember `json:"primaryMembers"`
		Protected      string        `json:"protected,omitempty"` // why it can't be deleted
	}{DN: dn, Members: []groupMember{}, PrimaryMembers: []groupMember{}}
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		g, err := c.GetGroup(dn)
		if err != nil {
			return err
		}
		gids, err := s.personGIDs(c)
		if err != nil {
			return err
		}
		out.Name, out.GID, out.Kind = g.Name, g.GID, kindOf(*g, gids)
		for _, m := range s.realMembers(g.Members) {
			mem := groupMember{DN: m, Name: m}
			if pe, err := c.GetAttrs(m, users.Attrs); err == nil {
				u := users.FromEntry(*pe)
				mem.Username, mem.Name = u.Username, u.Name
			}
			out.Members = append(out.Members, mem)
		}
		out.Members = append(out.Members, s.resolveUIDs(c, g.MemberUIDs)...)
		if g.GID != "" && !g.IsDNGroup() {
			es, err := c.SearchAttrs(s.c().Users.BaseDN, "(gidNumber="+ldapEscape(g.GID)+")", users.Attrs, 0)
			if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
				return err
			}
			for _, e := range es {
				u := users.FromEntry(e)
				out.PrimaryMembers = append(out.PrimaryMembers, groupMember{DN: u.DN, Username: u.Username, Name: u.Name})
			}
		}
		if eqFold(dn, s.c().Admin.GroupDN) {
			out.Protected = "This is the administrators group that controls who can sign in as an admin, so it can't be deleted here."
		} else if n := len(out.PrimaryMembers); n > 0 {
			out.Protected = strconv.Itoa(n) + " " + map[bool]string{true: "person uses", false: "people use"}[n == 1] +
				" this as their primary group, so it can't be deleted. Give them a different primary group first."
		}
		return nil
	}) {
		return
	}
	byName := func(m []groupMember) {
		sort.Slice(m, func(i, j int) bool { return strings.ToLower(m[i].Name) < strings.ToLower(m[j].Name) })
	}
	byName(out.Members)
	byName(out.PrimaryMembers)
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
	if s.withSvc(w, func(c *ldapclient.Client) error {
		g, err := c.GetGroup(dn)
		if err != nil {
			return err
		}
		// A Linux group that people use as their primary group must not disappear from under them.
		if g.GID != "" && !g.IsDNGroup() {
			es, err := c.SearchAttrs(s.c().Users.BaseDN, "(gidNumber="+ldapEscape(g.GID)+")", []string{"uid"}, 0)
			if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
				return err
			}
			if n := len(es); n > 0 {
				return validationError(strconv.Itoa(n) + " " + map[bool]string{true: "person uses", false: "people use"}[n == 1] +
					" this as their primary group, so it can't be deleted. Give them a different primary group first.")
			}
		}
		return c.Delete(dn)
	}) {
		audit(sess, "group.delete", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// changeMember adds or removes one account in a group of either kind.
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
			if add {
				return s.addMember(c, gdn, member)
			}
			return s.removeMember(c, gdn, member)
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

// chooseID returns the id typed by the admin (after checking it's free) or the next free one in the range.
func (s *Server) chooseID(c *ldapclient.Client, cfg *config.Config, override string) (int, error) {
	if override = strings.TrimSpace(override); override != "" {
		n, err := strconv.Atoi(override)
		if err != nil || n < 1000 || n > 2147483647 {
			return 0, validationError("The ID must be a whole number of at least 1000.")
		}
		used, err := idInUse(c, cfg, n)
		if err != nil {
			return 0, idError(err)
		}
		if used {
			return 0, validationError("The number " + strconv.Itoa(n) + " is already used by another account or group.")
		}
		return n, nil
	}
	return nextUnixID(c, cfg)
}
