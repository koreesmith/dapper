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
	if q.Get("format") == "csv" { // everyone who matches, not just one page
		writeUsersCSV(w, users.Filtered(all, users.Query{Search: q.Get("q"), Sort: q.Get("sort"), Desc: q.Get("dir") == "desc", Status: q.Get("status")}))
		return
	}
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
		groups, err := s.memberships(c, dn, u.Username, first(e.Values("gidNumber")))
		if err != nil {
			return err
		}
		if groups == nil {
			groups = []membership{}
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
		changes = append(changes, ldapclient.Change{Op: "replace", Attr: "cn", Values: syncFullName(cur, oldFull, full)})
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
	if strings.EqualFold(cfg.Users.RDNAttribute, "cn") {
		// The account is named cn=<username>, so that value has to be one of its cn values.
		attrs["cn"] = addUnique([]string{username}, full)
	}
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

// newPerson is everything needed to create an account.
type newPerson struct {
	Username string
	Attrs    map[string][]string
	Password string   // empty: generate a temporary one
	Groups   []string // DNs of groups to join
}

type createdPerson struct {
	DN           string
	Username     string
	TempPassword string // only when Dapper generated it
}

// validatePerson checks a request without touching the directory, so a bad row is reported before anything is created.
func (s *Server) validatePerson(cfg *config.Config, in newPerson) (string, map[string][]string, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	attrs, err := s.personAttrs(cfg, username, in.Attrs)
	if err != nil {
		return "", nil, err
	}
	if in.Password != "" {
		if err := s.checkPassword(in.Password); err != nil {
			return "", nil, err
		}
	}
	for _, g := range in.Groups {
		if _, err := s.groupDN(g); err != nil {
			return "", nil, err
		}
	}
	return username, attrs, nil
}

// createPerson makes an account: the entry, its password, the private Linux group, and any group memberships.
// If a later step fails the account is removed again, so nothing is left half-made.
func (s *Server) createPerson(c *ldapclient.Client, cfg *config.Config, in newPerson) (*createdPerson, error) {
	username, attrs, err := s.validatePerson(cfg, in)
	if err != nil {
		return nil, err
	}
	password, generated := in.Password, false
	if password == "" {
		password, generated = generatePassword(14), true
	}
	gid := 0
	if cfg.Unix.Enabled {
		unixMu.Lock() // keep "pick a free number" and "use it" together
		defer unixMu.Unlock()
		if gid, err = s.prepareUnix(c, cfg, username, attrs, first(in.Attrs["uidNumber"])); err != nil {
			return nil, err
		}
	}
	dn, err := s.addPerson(c, cfg, username, attrs, password)
	if err != nil {
		return nil, err
	}
	if cfg.Unix.Enabled {
		if err := addPrivateGroup(c, cfg, username, gid); err != nil {
			_ = c.Delete(dn) // no half-made account
			return nil, err
		}
	}
	for _, g := range in.Groups {
		if err := s.addMember(c, g, dn); err != nil {
			return nil, err
		}
	}
	out := &createdPerson{DN: dn, Username: username}
	if generated {
		out.TempPassword = password
	}
	return out, nil
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
	var made *createdPerson
	if !s.withSvc(w, func(c *ldapclient.Client) (err error) {
		made, err = s.createPerson(c, cfg, newPerson{Username: in.Username, Attrs: in.Attrs, Password: in.Password, Groups: in.Groups})
		return
	}) {
		return
	}
	audit(sess, "user.create", made.DN)
	resp := map[string]any{"dn": made.DN, "username": made.Username}
	if made.TempPassword != "" {
		resp["temporaryPassword"] = made.TempPassword
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

// suspendPerson suspends or restores an account and returns its (possibly new) DN. It can't be used on
// the person making the change.
func (s *Server) suspendPerson(c *ldapclient.Client, sess *session.Session, dn string, suspend bool) (string, error) {
	if suspend && strings.EqualFold(dn, sess.DN) {
		return dn, validationError("You can't suspend your own account.")
	}
	newDN := dn
	err := func() error {
		if s.c().Users.SuspendedBaseDN != "" {
			target := s.c().Users.SuspendedBaseDN
			if !suspend {
				target = s.c().Users.BaseDN
			}
			if s.isSuspendedDN(dn) == suspend {
				if !suspend {
					// Not suspended, but the password policy may have locked it after too many wrong
					// passwords: restoring clears that too.
					err := c.Modify(dn, []ldapclient.Change{{Op: "delete", Attr: "pwdAccountLockedTime"}})
					if ldapclient.IsCode(err, ldapclient.CodeNoSuchAttribute) || ldapclient.IsCode(err, ldapclient.CodeUndefinedAttribute) {
						return nil
					}
					return err
				}
				return nil // already suspended
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
	}()
	if err != nil {
		return dn, err
	}
	if suspend { // a suspended person must not carry on with a session they already had
		s.sessions.RevokeFor(dn, "")
		s.sessions.RevokeFor(newDN, "")
	}
	act := "user.restore"
	if suspend {
		act = "user.suspend"
	}
	audit(sess, act, newDN)
	return newDN, nil
}

func (s *Server) setSuspended(suspend bool) handler {
	return func(w http.ResponseWriter, r *http.Request, sess *session.Session) {
		dn, err := s.userDN(r.URL.Query().Get("dn"))
		if err != nil {
			failLDAP(w, err)
			return
		}
		var newDN string
		if !s.withSvc(w, func(c *ldapclient.Client) (err error) { newDN, err = s.suspendPerson(c, sess, dn, suspend); return }) {
			return
		}
		writeJSON(w, map[string]string{"dn": newDN})
	}
}

// ---- delete / password ----

// deletePerson removes an account, its memberships and its personal group. It can't be used on the person making the change.
func (s *Server) deletePerson(c *ldapclient.Client, sess *session.Session, dn string) error {
	if strings.EqualFold(dn, sess.DN) {
		return validationError("You can't delete your own account.")
	}
	cfg := s.c()
	var uid, gid string
	if e, err := c.GetAttrs(dn, []string{"uid", "gidNumber"}); err == nil {
		uid, gid = first(e.Values("uid")), first(e.Values("gidNumber"))
	}
	if err := s.replaceInGroups(c, dn, ""); err != nil {
		return err
	}
	if err := c.Delete(dn); err != nil {
		return err
	}
	if cfg.Groups.BaseDN != "" {
		// Tidy Linux groups: take their username out of every group's member list, and remove
		// their personal group (a group named after them with their id, not shared with anyone else).
		if err := s.removeUIDFromGroups(c, uid); err != nil {
			return err
		}
		if err := removePrivateGroup(c, cfg, uid, gid); err != nil {
			return err
		}
	}
	s.sessions.RevokeFor(dn, "")
	audit(sess, "user.delete", dn)
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	if !s.withSvc(w, func(c *ldapclient.Client) error { return s.deletePerson(c, sess, dn) }) {
		return
	}
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
	// Whoever may have been using the account (for example after a compromise) is signed out.
	s.sessions.RevokeFor(dn, "")
	audit(sess, "user.password.reset", dn)
	resp := map[string]any{"status": "ok"}
	if generated {
		resp["temporaryPassword"] = password
	}
	writeJSON(w, resp)
}

// syncFullName returns the new set of cn values after a name change: the old full name is
// replaced by the new one, and every other value is kept. In particular the value the
// entry is named by (for example cn=koree in cn=koree,ou=People,...) must never be
// removed, or the directory refuses the change with a naming violation.
func syncFullName(cur *ldapclient.Entry, oldFull, full string) []string {
	rdnAttr, rdnVal := rdnParts(cur.DN)
	protected := func(v string) bool { return strings.EqualFold(rdnAttr, "cn") && strings.EqualFold(v, rdnVal) }
	var next []string
	for _, v := range cur.Values("cn") {
		if strings.EqualFold(v, oldFull) && !protected(v) {
			continue
		}
		next = append(next, v)
	}
	for _, v := range next {
		if strings.EqualFold(v, full) {
			return next
		}
	}
	return append(next, full)
}
