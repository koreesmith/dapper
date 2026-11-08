package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
	"github.com/koreesmith/dapper/backend/internal/users"
)

// maxUsers bounds how many accounts one search loads into memory.
const maxUsers = 5000

// forbiddenError is a request the caller is not permitted to make.
type forbiddenError string

func (e forbiddenError) Error() string { return string(e) }

// ---- list ----

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	var all []users.User
	truncated := false
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		bases := []string{s.c().Users.BaseDN}
		if s.c().Users.SuspendedBaseDN != "" {
			bases = append(bases, s.c().Users.SuspendedBaseDN)
		}
		for _, base := range bases {
			entries, trunc, err := c.ListPeople(base, users.Attrs, maxUsers)
			if err != nil {
				if ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) && base != s.c().Users.BaseDN {
					continue // suspended container not created yet
				}
				return err
			}
			truncated = truncated || trunc
			for _, e := range entries {
				u := users.FromEntry(e)
				u.Suspended = u.Suspended || s.isSuspendedDN(u.DN)
				all = append(all, u)
			}
		}
		return nil
	}) {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	res := users.List(all, users.Query{
		Search: q.Get("q"), Sort: q.Get("sort"), Desc: q.Get("dir") == "desc",
		Status: q.Get("status"), Page: page, PageSize: size,
	})
	writeJSON(w, map[string]any{
		"users": res.Users, "total": res.Total, "page": res.Page, "pageSize": res.PageSize, "truncated": truncated,
	})
}

// ---- detail ----

type fieldValue struct {
	policy.Field
	Values []string `json:"values"`
}

func (s *Server) fieldValues(e *ldapclient.Entry, keep func(policy.Field) bool) []fieldValue {
	var out []fieldValue
	for _, f := range s.catalog() {
		// Unix details only make sense on accounts that are Unix accounts.
		if f.Feature == "unix" && !hasObjectClass(e, "posixAccount") {
			continue
		}
		if keep(f) {
			v := e.Values(f.Attr)
			if v == nil {
				v = []string{}
			}
			out = append(out, fieldValue{Field: f, Values: v})
		}
	}
	return out
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	var resp map[string]any
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		e, err := c.GetAttrs(dn, append([]string{"*"}, "pwdAccountLockedTime"))
		if err != nil {
			return err
		}
		u := users.FromEntry(*e)
		gs, err := s.memberships(c, dn)
		if err != nil {
			return err
		}
		groups := make([]map[string]string, 0, len(gs))
		for _, g := range gs {
			groups = append(groups, map[string]string{"dn": g.DN, "name": g.Name})
		}
		resp = map[string]any{
			"dn": dn, "username": u.Username, "name": u.Name,
			"suspended": u.Suspended || s.isSuspendedDN(dn),
			"isSelf":    strings.EqualFold(dn, sess.DN),
			"fields":    s.fieldValues(e, func(policy.Field) bool { return true }),
			"groups":    groups,
		}
		return nil
	}) {
		return
	}
	writeJSON(w, resp)
}

// ---- change building (shared with self-service) ----

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func first(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

// buildChanges validates input against the field catalog and returns only
// the modifications that actually differ from the current entry.
func (s *Server) buildChanges(cur *ldapclient.Entry, in map[string][]string, allow func(policy.Field) bool) ([]ldapclient.Change, error) {
	var changes []ldapclient.Change
	want := map[string][]string{}
	for attr, vals := range in {
		f, ok := s.field(attr)
		if !ok {
			return nil, validationError("Unknown field: " + attr)
		}
		if f.ReadOnly {
			return nil, forbiddenError(f.Label + " can't be changed here.")
		}
		if f.Feature == "unix" && !hasObjectClass(cur, "posixAccount") {
			return nil, validationError("This account isn't a Unix account, so its Unix login details can't be changed here.")
		}
		if !allow(f) {
			return nil, forbiddenError("You aren't allowed to change " + f.Label + ".")
		}
		vals = policy.Clean(vals)
		if !f.Multi && len(vals) > 1 {
			vals = vals[:1]
		}
		if err := policy.Validate(f, vals); err != nil {
			return nil, validationError(err.Error())
		}
		if f.Required && len(vals) == 0 {
			return nil, validationError(f.Label + " can't be empty.")
		}
		if sameSet(cur.Values(f.Attr), vals) {
			continue
		}
		want[f.Attr] = vals
		changes = append(changes, ldapclient.Change{Op: "replace", Attr: f.Attr, Values: vals})
	}
	// A first SSH key needs the auxiliary class that allows the attribute.
	if keys := want["sshPublicKey"]; len(keys) > 0 && !hasObjectClass(cur, "ldapPublicKey") {
		changes = append([]ldapclient.Change{{Op: "add", Attr: "objectClass", Values: []string{"ldapPublicKey"}}}, changes...)
	}
	// Keep the full name (cn) and, if it was auto-derived, the display name in step.
	gn, hasGN := want["givenName"]
	sn, hasSN := want["sn"]
	if hasGN || hasSN {
		g, n := first(cur.Values("givenName")), first(cur.Values("sn"))
		if hasGN {
			g = first(gn)
		}
		if hasSN {
			n = first(sn)
		}
		oldFull := strings.TrimSpace(first(cur.Values("givenName")) + " " + first(cur.Values("sn")))
		full := strings.TrimSpace(g + " " + n)
		changes = append(changes, ldapclient.Change{Op: "replace", Attr: "cn", Values: []string{full}})
		if _, explicit := want["displayName"]; !explicit && first(cur.Values("displayName")) == oldFull {
			changes = append(changes, ldapclient.Change{Op: "replace", Attr: "displayName", Values: []string{full}})
		}
	}
	return changes, nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	var in struct {
		Attrs map[string][]string `json:"attrs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	var changed int
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		cur, err := c.GetAttrs(dn, []string{"*"})
		if err != nil {
			return err
		}
		changes, err := s.buildChanges(cur, in.Attrs, func(policy.Field) bool { return true })
		if err != nil {
			return err
		}
		changed = len(changes)
		if changed == 0 {
			return nil
		}
		return c.Modify(dn, changes)
	}) {
		return
	}
	if changed > 0 {
		audit(sess, "user.update", dn)
	}
	writeJSON(w, map[string]any{"status": "ok", "changed": changed})
}

// ---- create ----

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

// personAttrs validates input and builds the attributes for a new account.
func (s *Server) personAttrs(cfg *config.Config, username string, in map[string][]string) (map[string][]string, error) {
	if !usernameRE.MatchString(username) {
		return nil, validationError("Usernames must be 2\u201332 characters: lowercase letters, numbers, dots, dashes or underscores, starting with a letter or number.")
	}
	attrs := map[string][]string{}
	for _, f := range s.catalogFor(cfg) {
		if f.ReadOnly {
			continue // ids are allocated, never typed in
		}
		vals := policy.Clean(in[f.Attr])
		if !f.Multi && len(vals) > 1 {
			vals = vals[:1]
		}
		if f.Required && len(vals) == 0 {
			return nil, validationError(f.Label + " is required.")
		}
		if err := policy.Validate(f, vals); err != nil {
			return nil, validationError(err.Error())
		}
		if len(vals) > 0 {
			attrs[f.Attr] = vals
		}
	}
	full := strings.TrimSpace(first(attrs["givenName"]) + " " + first(attrs["sn"]))
	attrs["cn"] = []string{full}
	if _, ok := attrs["displayName"]; !ok {
		attrs["displayName"] = []string{full}
	}
	attrs["uid"] = []string{username}
	attrs["objectClass"] = cfg.Users.ObjectClasses
	return attrs, nil
}

// addPerson creates an account and sets its password, undoing the account if the
// password can't be set. Usernames must be unique across active and suspended people.
func (s *Server) addPerson(c *ldapclient.Client, cfg *config.Config, username string, attrs map[string][]string, password string) (string, error) {
	for _, base := range []string{cfg.Users.BaseDN, cfg.Users.SuspendedBaseDN} {
		if base == "" {
			continue
		}
		es, err := c.SearchAttrs(base, "(uid="+ldapEscape(username)+")", []string{"uid"}, 1)
		if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			return "", err
		}
		if len(es) > 0 {
			return "", validationError("The username \u201c" + username + "\u201d is already taken.")
		}
	}
	dn := cfg.Users.RDNAttribute + "=" + escapeRDN(username) + "," + cfg.Users.BaseDN
	if err := c.Add(ldapclient.Entry{DN: dn, Attrs: attrs}); err != nil {
		return "", err
	}
	if err := c.SetPassword(dn, password); err != nil {
		_ = c.Delete(dn) // don't leave a half-created account behind
		return "", err
	}
	return dn, nil
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		Username string              `json:"username"`
		Attrs    map[string][]string `json:"attrs"`
		Password string              `json:"password"`
		Groups   []string            `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.c()
	username := strings.ToLower(strings.TrimSpace(in.Username))
	attrs, err := s.personAttrs(cfg, username, in.Attrs)
	if err != nil {
		failLDAP(w, err)
		return
	}
	password, generated := in.Password, false
	if password == "" {
		password, generated = generatePassword(14), true
	} else if err := s.checkPassword(password); err != nil {
		failLDAP(w, err)
		return
	}
	for _, g := range in.Groups {
		if _, err := s.groupDN(g); err != nil {
			failLDAP(w, err)
			return
		}
	}

	var dn string
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		var err error
		gid := 0
		if cfg.Unix.Enabled {
			unixMu.Lock() // keep "pick a free number" and "use it" together
			defer unixMu.Unlock()
			if gid, err = s.prepareUnix(c, cfg, username, attrs, first(in.Attrs["uidNumber"])); err != nil {
				return err
			}
		}
		if dn, err = s.addPerson(c, cfg, username, attrs, password); err != nil {
			return err
		}
		if cfg.Unix.Enabled {
			if err := addPrivateGroup(c, cfg, username, gid); err != nil {
				_ = c.Delete(dn) // no half-made account
				return err
			}
		}
		for _, g := range in.Groups {
			e, err := c.GetAttrs(g, []string{cfg.Groups.MemberAttribute})
			if err != nil {
				return err
			}
			if err := s.setMembers(c, g, append(s.realMembers(e.Values(cfg.Groups.MemberAttribute)), dn)); err != nil {
				return err
			}
		}
		return nil
	}) {
		return
	}
	audit(sess, "user.create", dn)
	resp := map[string]any{"dn": dn, "username": username}
	if generated {
		resp["temporaryPassword"] = password
	}
	writeJSON(w, resp)
}

// ---- suspend / restore ----

func rdnOf(dn string) string {
	for i := 0; i < len(dn); i++ {
		if dn[i] == '\\' {
			i++
		} else if dn[i] == ',' {
			return dn[:i]
		}
	}
	return dn
}

func (s *Server) setSuspended(suspend bool) handler {
	return func(w http.ResponseWriter, r *http.Request, sess *session.Session) {
		dn, err := s.userDN(r.URL.Query().Get("dn"))
		if err != nil {
			failLDAP(w, err)
			return
		}
		if suspend && strings.EqualFold(dn, sess.DN) {
			fail(w, http.StatusBadRequest, "You can't suspend your own account.")
			return
		}
		newDN := dn
		if !s.withSvc(w, func(c *ldapclient.Client) error {
			if s.c().Users.SuspendedBaseDN != "" {
				target := s.c().Users.SuspendedBaseDN
				if !suspend {
					target = s.c().Users.BaseDN
				}
				if s.isSuspendedDN(dn) == suspend {
					return nil // already in the requested state
				}
				if err := c.Move(dn, rdnOf(dn), target); err != nil {
					return err
				}
				newDN = rdnOf(dn) + "," + target
				return s.replaceInGroups(c, dn, newDN)
			}
			// ppolicy mode: lock or unlock in place.
			ch := ldapclient.Change{Op: "delete", Attr: "pwdAccountLockedTime"}
			if suspend {
				ch = ldapclient.Change{Op: "replace", Attr: "pwdAccountLockedTime", Values: []string{"000001010000Z"}}
			}
			err := c.Modify(dn, []ldapclient.Change{ch})
			if !suspend && ldapclient.IsCode(err, ldapclient.CodeNoSuchAttribute) {
				return nil // wasn't locked
			}
			if ldapclient.IsCode(err, ldapclient.CodeUndefinedAttribute) || ldapclient.IsCode(err, ldapclient.CodeObjectClassViolation) {
				return validationError("This directory can't lock accounts in place (the ppolicy overlay isn't enabled). Ask your administrator to set users.suspendedBaseDN so suspended accounts can be moved aside instead.")
			}
			return err
		}) {
			return
		}
		act := "user.restore"
		if suspend {
			act = "user.suspend"
		}
		audit(sess, act, newDN)
		writeJSON(w, map[string]string{"dn": newDN})
	}
}

// ---- delete / password ----

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	if strings.EqualFold(dn, sess.DN) {
		fail(w, http.StatusBadRequest, "You can't delete your own account.")
		return
	}
	cfg := s.c()
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		var uid, gid string
		if cfg.Unix.Enabled {
			if e, err := c.GetAttrs(dn, []string{"uid", "gidNumber"}); err == nil {
				uid, gid = first(e.Values("uid")), first(e.Values("gidNumber"))
			}
		}
		if err := s.replaceInGroups(c, dn, ""); err != nil {
			return err
		}
		if err := c.Delete(dn); err != nil {
			return err
		}
		return removePrivateGroup(c, cfg, uid, gid)
	}) {
		return
	}
	audit(sess, "user.delete", dn)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	var in struct{ Password string }
	_ = json.NewDecoder(r.Body).Decode(&in)
	password, generated := in.Password, false
	if password == "" {
		password, generated = generatePassword(14), true
	} else if err := s.checkPassword(password); err != nil {
		failLDAP(w, err)
		return
	}
	if !s.withSvc(w, func(c *ldapclient.Client) error { return c.SetPassword(dn, password) }) {
		return
	}
	audit(sess, "user.password.reset", dn)
	resp := map[string]any{"status": "ok"}
	if generated {
		resp["temporaryPassword"] = password
	}
	writeJSON(w, resp)
}
