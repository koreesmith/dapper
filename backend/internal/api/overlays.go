package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/overlays"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// The Server page manages the directory server's own features (OpenLDAP overlays) by editing
// cn=config. That needs a separate "configuration administrator" login, which an admin types
// in for the session. It is kept only in memory and forgotten after 15 idle minutes.
const configIdle = 15 * time.Minute

func (s *Server) overlayContext() overlays.Context {
	cfg := s.c()
	ctx := overlays.Context{
		Suffix: suffixOf(cfg.Users.BaseDN), UsersBase: cfg.Users.BaseDN, GroupsBase: cfg.Groups.BaseDN,
		GroupClass: cfg.Groups.ObjectClass, MemberAttr: cfg.Groups.MemberAttribute,
	}
	if cfg.Groups.BaseDN != "" {
		ctx.PlaceholderDN = s.placeholderDN()
	}
	return ctx
}

// ---- unlocking ----

func (s *Server) overlaysUnlock(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ DN, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.DN) == "" || in.Password == "" {
		fail(w, http.StatusBadRequest, "Enter the configuration administrator's name and password.")
		return
	}
	key := "cfg:" + strings.ToLower(sess.DN)
	if s.limit.blocked(key) {
		fail(w, http.StatusTooManyRequests, "Too many incorrect attempts. Wait a few minutes and try again.")
		return
	}
	c, err := ldapclient.Dial(s.server())
	if err == nil {
		err = c.Bind(strings.TrimSpace(in.DN), in.Password)
	}
	if err == nil {
		_, err = c.GetAttrs("cn=config", []string{"1.1"})
	}
	if err != nil {
		if c != nil {
			c.Close()
		}
		s.limit.fail(key)
		// Never log the password; the name and the server's answer are what help to diagnose this.
		log.Printf("overlays unlock failed: configuration login %q (signed in as %q): %v", strings.TrimSpace(in.DN), sess.DN, err)
		msg := "The directory server didn't accept that name and password for its configuration. Check that the name is exactly the server's configuration root name (olcRootDN) and that a password has been set for it: this is a separate login from your directory manager. The Dapper log has the server's answer."
		if !strings.Contains(err.Error(), "nvalid") && err != ldapclient.ErrInvalidCredentials {
			msg = "Couldn't read the server's configuration with that account. It needs to be allowed to read and change cn=config (see the help below). The Dapper log has the server's answer."
		}
		fail(w, http.StatusUnauthorized, msg)
		return
	}
	s.limit.reset(key)
	sess.SetConfigConn(c)
	audit(sess, "overlays.unlock", "cn=config")
	writeJSON(w, map[string]bool{"unlocked": true})
}

func (s *Server) overlaysLock(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	sess.ClearConfigConn()
	writeJSON(w, map[string]bool{"unlocked": false})
}

// ---- reading the server's state ----

func loadOverlayState(cc *ldapclient.Client, usersBase string) (*overlays.State, error) {
	dbs, err := cc.SearchAttrs("cn=config", "(&(objectClass=olcDatabaseConfig)(olcSuffix=*))", []string{"olcSuffix"}, 0)
	if err != nil {
		return nil, err
	}
	st := &overlays.State{Overlays: map[string]*overlays.Overlay{}}
	best := -1
	for _, d := range dbs {
		for _, suf := range d.Values("olcSuffix") {
			if strings.HasSuffix(strings.ToLower(usersBase), strings.ToLower(suf)) && len(suf) > best {
				best, st.DBDN, st.Suffix = len(suf), d.DN, suf
			}
		}
	}
	if st.DBDN == "" {
		return nil, validationError("Couldn't find which database on the server holds your people container (" + usersBase + ").")
	}
	ovs, err := cc.SearchAttrs(st.DBDN, "(objectClass=olcOverlayConfig)", []string{"*"}, 0)
	if err != nil {
		return nil, err
	}
	for _, o := range ovs {
		id := overlays.OverlayID(first(o.Values("olcOverlay")))
		if id != "" {
			st.Overlays[id] = &overlays.Overlay{DN: o.DN, Values: o.Attrs}
		}
	}
	mods, err := cc.SearchAttrs("cn=config", "(objectClass=olcModuleList)", []string{"olcModuleLoad"}, 0)
	if err != nil {
		return nil, err
	}
	for _, m := range mods {
		if st.ModuleDN == "" {
			st.ModuleDN = m.DN
		}
		st.Modules = append(st.Modules, m.Values("olcModuleLoad")...)
	}
	return st, nil
}

func (s *Server) policyFacts(c *ldapclient.Client, vals map[string]string, ctx overlays.Context) overlays.Facts {
	f := overlays.Facts{}
	if names, err := c.ObjectClassNames(); err == nil && len(names) > 0 {
		f.SchemaHas = names
	}
	pdn := strings.TrimSpace(vals["policyDN"])
	if pdn == "" {
		pdn = "cn=default,ou=policies," + ctx.Suffix
	}
	if ok, err := c.Exists(pdn); err == nil {
		f.PolicyExists = ok
	}
	if i := strings.Index(pdn, ","); i > 0 {
		if ok, err := c.Exists(pdn[i+1:]); err == nil {
			f.PolicyOUExists = ok
		}
	}
	return f
}

type overlayView struct {
	overlays.Def
	Enabled *bool             `json:"enabled"` // null when the server's configuration hasn't been unlocked
	Values  map[string]string `json:"values"`
	Notes   []string          `json:"notes,omitempty"` // things worth knowing about how it's set up
}

// overlayNotes points out settings that look wrong for how Dapper manages groups.
func overlayNotes(id string, vals map[string]string, ctx overlays.Context) []string {
	if id == "memberof" && ctx.GroupsBase != "" {
		oc, ad := vals["groupOC"], vals["memberAD"]
		if oc != "" && !strings.EqualFold(oc, ctx.GroupClass) {
			return []string{"memberOf is set up for “" + oc + "” groups, but the groups Dapper manages are “" + ctx.GroupClass + "” groups, so memberOf won't be filled in for them. Use Change to switch it to " + ctx.GroupClass + "."}
		}
		if ad != "" && !strings.EqualFold(ad, ctx.MemberAttr) {
			return []string{"memberOf is watching the “" + ad + "” attribute, but your groups list members in “" + ctx.MemberAttr + "”. Use Change to fix it."}
		}
	}
	return nil
}

func (s *Server) overlaysGet(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	ctx := s.overlayContext()
	defs := overlays.Catalog(ctx)
	out := map[string]any{"unlocked": false, "suffix": ctx.Suffix}
	views := make([]overlayView, len(defs))
	for i, d := range defs {
		views[i] = overlayView{Def: d, Values: map[string]string{}}
	}
	if cc := sess.ConfigConn(configIdle); cc != nil {
		st, err := loadOverlayState(cc, ctx.UsersBase)
		if err != nil {
			// The connection died or lost its rights: ask for the login again.
			sess.ClearConfigConn()
			if _, isInput := err.(validationError); isInput {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			out["unlockError"] = "The configuration login stopped working. Please unlock again."
		} else {
			out["unlocked"], out["database"] = true, st.DBDN
			if pe := st.Overlays["ppolicy"]; overlays.Active("ppolicy", pe) {
				s.setPolicyDN(first(pe.Values["olcPPolicyDefault"]))
			} else {
				s.setPolicyDN("")
			}
			var policy map[string][]string
			if pe := st.Overlays["ppolicy"]; pe != nil {
				if pdn := first(pe.Values["olcPPolicyDefault"]); pdn != "" {
					if svc, err := s.svc(); err == nil {
						if e, err := svc.GetAttrs(pdn, []string{"*"}); err == nil {
							policy = e.Attrs
						}
						svc.Close()
					}
				}
			}
			for i := range views {
				ov := st.Overlays[views[i].ID]
				on := overlays.Active(views[i].ID, ov)
				views[i].Enabled = &on
				if on {
					views[i].Values = overlays.Values(views[i].ID, ov, policy)
					views[i].Notes = overlayNotes(views[i].ID, views[i].Values, ctx)
				}
			}
		}
	}
	out["overlays"] = views
	writeJSON(w, out)
}

// ---- planning and applying ----

type overlayRequest struct {
	Overlay   string            `json:"overlay"`
	Action    string            `json:"action"`
	Values    map[string]string `json:"values"`
	DBDN      string            `json:"dbDN"`      // when working without the configuration login
	Confirmed bool              `json:"confirmed"` // the admin approved the changes
	Refresh   bool              `json:"refresh"`   // memberOf: also refresh existing groups
}

func overlayPlanError(err error) (int, string) {
	if overlays.IsInputError(err) {
		return http.StatusBadRequest, err.Error()
	}
	if ve, ok := err.(validationError); ok {
		return http.StatusBadRequest, ve.Error()
	}
	return 0, ""
}

// buildOverlayPlan plans a change using the live state when unlocked, or assumptions when not.
func (s *Server) buildOverlayPlan(sess *session.Session, req overlayRequest) (*overlays.Plan, bool, error) {
	ctx := s.overlayContext()
	var st *overlays.State
	unlocked := false
	if cc := sess.ConfigConn(configIdle); cc != nil {
		var err error
		if st, err = loadOverlayState(cc, ctx.UsersBase); err != nil {
			return nil, false, err
		}
		unlocked = true
	} else {
		db := strings.TrimSpace(req.DBDN)
		if db == "" {
			db = "olcDatabase={1}mdb,cn=config"
		}
		st = &overlays.State{DBDN: db, Suffix: ctx.Suffix, Overlays: map[string]*overlays.Overlay{}}
		if req.Action != "enable" {
			st.Overlays[req.Overlay] = &overlays.Overlay{DN: "olcOverlay=" + req.Overlay + "," + db}
		}
	}
	facts := overlays.Facts{}
	if req.Overlay == "ppolicy" {
		if svc, err := s.svc(); err == nil {
			facts = s.policyFacts(svc, req.Values, ctx)
			svc.Close()
		}
	}
	plan, err := overlays.BuildPlan(ctx, st, req.Overlay, req.Action, req.Values, facts)
	if err != nil {
		return nil, unlocked, err
	}
	s.addOverlayAdvice(plan, req, ctx)
	return plan, unlocked, nil
}

// addOverlayAdvice adds warnings that need a look at the live directory.
func (s *Server) addOverlayAdvice(plan *overlays.Plan, req overlayRequest, ctx overlays.Context) {
	if req.Action == "disable" {
		return
	}
	svc, err := s.svc()
	if err != nil {
		return
	}
	defer svc.Close()
	switch req.Overlay {
	case "unique":
		attrs := strings.Split(req.Values["attrs"], ",")
		var found []string
		for _, a := range attrs {
			a = strings.TrimSpace(a)
			if a == "" {
				continue
			}
			es, err := svc.SearchComplete(ctx.UsersBase, "("+a+"=*)", []string{a})
			if err != nil {
				continue
			}
			seen := map[string]int{}
			for _, e := range es {
				for _, v := range e.Values(a) {
					seen[strings.ToLower(v)]++
				}
			}
			var dups []string
			for v, n := range seen {
				if n > 1 {
					dups = append(dups, fmt.Sprintf("“%s” (%d people)", v, n))
				}
			}
			sort.Strings(dups)
			if len(dups) > 0 {
				if len(dups) > 3 {
					dups = append(dups[:3], fmt.Sprintf("and %d more", len(dups)-3))
				}
				found = append(found, a+": "+strings.Join(dups, ", "))
			}
		}
		if len(found) > 0 {
			plan.Warnings = append(plan.Warnings, "Some people already share a value, and they won't be changed. Fix these yourself: "+strings.Join(found, "; ")+".")
		}
	case "memberof":
		if req.Action == "enable" && ctx.GroupsBase != "" {
			if gs, err := svc.AllGroups(ctx.GroupsBase); err == nil {
				n := 0
				for _, g := range gs {
					if g.IsDNGroup() && len(s.realMembers(g.Members)) > 0 {
						n++
					}
				}
				if n > 0 {
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("%d of your groups already have members. Choose “refresh existing groups” below so their members show memberOf straight away.", n))
				}
			}
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s *Server) overlaysPreview(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req overlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	plan, unlocked, err := s.buildOverlayPlan(sess, req)
	if err != nil {
		if code, msg := overlayPlanError(err); code != 0 {
			fail(w, code, msg)
			return
		}
		failLDAP(w, err)
		return
	}
	if plan.Summary == nil {
		plan.Summary = []string{}
	}
	if plan.Warnings == nil {
		plan.Warnings = []string{} // always a list: the UI iterates over it
	}
	resp := map[string]any{
		"summary": plan.Summary, "warnings": plan.Warnings, "unlocked": unlocked,
		"ldif": plan.LDIF("config"), "dataLDIF": plan.LDIF("data"),
		"commands": map[string]string{},
	}
	cmds := map[string]string{}
	if plan.HasOps("config") {
		cmds["config"] = "sudo ldapmodify -Y EXTERNAL -H ldapi:/// -f dapper-server-change.ldif"
	}
	if plan.HasOps("data") {
		cmds["data"] = "ldapmodify -x -H ldap://localhost -D " + shellQuote("<your directory manager DN>") + " -W -f dapper-directory-change.ldif"
	}
	resp["commands"] = cmds
	writeJSON(w, resp)
}

func (s *Server) overlaysApply(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req overlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if !req.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve the changes before applying them.")
		return
	}
	cc := sess.ConfigConn(configIdle)
	if cc == nil {
		fail(w, http.StatusConflict, "The server configuration is locked. Unlock it again to make changes.")
		return
	}
	plan, _, err := s.buildOverlayPlan(sess, req)
	if err != nil {
		if code, msg := overlayPlanError(err); code != 0 {
			fail(w, code, msg)
			return
		}
		failLDAP(w, err)
		return
	}
	ctx := s.overlayContext()
	st, err := loadOverlayState(cc, ctx.UsersBase)
	if err != nil {
		failLDAP(w, err)
		return
	}
	backup, err := s.backupConfig(cc, st)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save a backup of the server's current configuration, so nothing was changed: "+err.Error())
		return
	}

	svc, err := s.svc()
	if err != nil {
		fail(w, http.StatusBadGateway, "Dapper can't reach the directory right now.")
		return
	}
	defer svc.Close()
	done := 0
	for _, op := range plan.Ops {
		conn := cc
		if op.Target == "data" {
			conn = svc
		}
		if err := applyOp(conn, op); err != nil {
			audit(sess, "overlays.failed", req.Overlay, "action", req.Action, "step", fmt.Sprint(done+1))
			code, msg := friendlyOverlayError(err, req.Overlay)
			writeJSONStatus(w, code, map[string]any{
				"error":  fmt.Sprintf("%s %d of %d steps were completed before this. A copy of the previous configuration is saved as %s.", msg, done, len(plan.Ops), backup),
				"backup": backup,
			})
			return
		}
		done++
	}
	refreshed := 0
	if req.Refresh && req.Overlay == "memberof" && req.Action == "enable" {
		refreshed, _ = s.refreshMemberOf(svc)
	}
	if req.Overlay == "ppolicy" {
		pdn := ""
		if req.Action != "disable" {
			if pdn = strings.TrimSpace(req.Values["policyDN"]); pdn == "" {
				pdn = "cn=default,ou=policies," + ctx.Suffix
			}
		}
		s.setPolicyDN(pdn)
	}
	audit(sess, "overlays."+req.Action, req.Overlay)
	writeJSON(w, map[string]any{"ok": true, "backup": backup, "refreshedGroups": refreshed})
}

func applyOp(c *ldapclient.Client, op overlays.Op) error {
	switch op.Kind {
	case "add":
		return c.Add(ldapclient.Entry{DN: op.DN, Attrs: op.Attrs})
	case "delete":
		return c.Delete(op.DN)
	case "modify":
		ch := make([]ldapclient.Change, len(op.Mods))
		for i, m := range op.Mods {
			ch[i] = ldapclient.Change{Op: m.Op, Attr: m.Attr, Values: m.Values}
		}
		return c.Modify(op.DN, ch)
	}
	return fmt.Errorf("unknown operation %q", op.Kind)
}

func friendlyOverlayError(err error, id string) (int, string) {
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "module") || strings.Contains(low, "olcmoduleload"):
		return http.StatusBadGateway, "The directory server couldn't load the “" + id + "” module. It may not be installed on that server (it's normally part of the slapd/openldap-servers package or its contrib add-on). Details: " + err.Error() + "."
	case ldapclient.IsCode(err, ldapclient.CodeInsufficientAccess):
		return http.StatusForbidden, "The configuration account isn't allowed to make this change."
	case ldapclient.IsCode(err, ldapclient.CodeAlreadyExists):
		return http.StatusConflict, "That is already set up on the server."
	case ldapclient.IsCode(err, ldapclient.CodeNoSuchObject):
		return http.StatusNotFound, "Something the change needed no longer exists on the server (it may have been changed elsewhere). Reload and try again."
	}
	code, msg := friendlyError(err)
	// An administrator can use the server's own explanation, so pass it on.
	var le *ldap.Error
	if errors.As(err, &le) && le.Err != nil {
		if d := strings.TrimSpace(le.Err.Error()); d != "" && !strings.Contains(msg, d) {
			msg += " The server said: " + d + "."
		}
	}
	return code, msg
}

// ---- backups ----

var backupNameRE = regexp.MustCompile(`^cn-config-[0-9]{8}-[0-9]{6}\.ldif$`)

func ldifEntry(e ldapclient.Entry) string {
	p := overlays.Plan{Ops: []overlays.Op{{Target: "config", Kind: "add", DN: e.DN, Attrs: e.Attrs}}}
	return strings.Replace(p.LDIF("config"), "changetype: add\n", "", 1)
}

// backupConfig writes the affected parts of cn=config (the database's settings and overlays, and the
// module list) to the data directory, so a change can be undone by hand.
func (s *Server) backupConfig(cc *ldapclient.Client, st *overlays.State) (string, error) {
	var b strings.Builder
	b.WriteString("# Saved by Dapper before changing the directory server's configuration.\n")
	b.WriteString("# Restore with: sudo ldapmodify -Y EXTERNAL -H ldapi:/// (review first).\n# Password hashes and other secrets are deliberately left out.\n\n")
	for _, base := range []string{st.DBDN, st.ModuleDN} {
		if base == "" {
			continue
		}
		es, err := cc.SearchAttrs(base, "(objectClass=*)", []string{"*"}, 0)
		if err != nil {
			return "", err
		}
		sort.Slice(es, func(i, j int) bool { return len(es[i].DN) < len(es[j].DN) })
		for _, e := range es {
			b.WriteString(ldifEntry(e) + "\n")
		}
	}
	dir := filepath.Join(s.c().DataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "cn-config-" + time.Now().Format("20060102-150405") + ".ldif"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Server) overlaysBackup(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	// Backups describe how the server is set up, so they need the same login as changing it.
	if sess.ConfigConn(configIdle) == nil {
		fail(w, http.StatusForbidden, "Unlock the server settings to download a backup.")
		return
	}
	name := r.URL.Query().Get("name")
	if !backupNameRE.MatchString(name) {
		fail(w, http.StatusBadRequest, "Unknown backup.")
		return
	}
	b, err := os.ReadFile(filepath.Join(s.c().DataDir, "backups", name))
	if err != nil {
		fail(w, http.StatusNotFound, "That backup doesn't exist.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(b)
}

// ---- refreshing memberOf for existing groups ----

// refreshMemberOf makes the memberOf overlay notice groups that already had members when it was
// turned on, by deleting and re-adding each group's members in one atomic change.
func (s *Server) refreshMemberOf(svc *ldapclient.Client) (int, error) {
	cfg := s.c()
	if cfg.Groups.BaseDN == "" {
		return 0, nil
	}
	gs, err := svc.AllGroups(cfg.Groups.BaseDN)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, g := range gs {
		if !g.IsDNGroup() {
			continue
		}
		members := s.realMembers(g.Members)
		if len(members) == 0 {
			continue
		}
		attr := g.DNMemberAttr(cfg.Groups.MemberAttribute)
		all := g.Members // include the placeholder, so the group is never empty in between
		if err := svc.Modify(g.DN, []ldapclient.Change{
			{Op: "delete", Attr: attr, Values: all}, {Op: "add", Attr: attr, Values: all},
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *Server) overlaysRefreshMemberOf(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ Confirmed bool }
	_ = json.NewDecoder(r.Body).Decode(&in)
	if !in.Confirmed {
		fail(w, http.StatusBadRequest, "Please approve refreshing the groups first.")
		return
	}
	var n int
	if s.withSvc(w, func(c *ldapclient.Client) (err error) { n, err = s.refreshMemberOf(c); return }) {
		audit(sess, "overlays.memberof.refresh", fmt.Sprint(n, " groups"))
		writeJSON(w, map[string]int{"refreshedGroups": n})
	}
}

// setPolicyDN records which entry holds the directory's password rules (empty when they're switched off),
// so people are told the rules that are really in force. Settings locked by the environment are left alone.
func (s *Server) setPolicyDN(dn string) {
	cur := s.c()
	if cur.Password.PolicyDN == dn {
		return
	}
	next := *cur
	next.Password.PolicyDN = dn
	if err := s.store.Save(&next); err != nil {
		log.Printf("couldn't record the password policy location: %v", err)
	}
}
