// Package overlays describes the OpenLDAP overlays Dapper can switch on and off, and
// turns a request ("turn on memberOf", "change the password rules") into a plan: the exact
// directory changes, in plain words and as LDIF. The same plan is previewed to the
// administrator and then applied, so what they approve is what runs. This package does
// no network I/O.
package overlays

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-ldap/ldap/v3"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Param is one question in an overlay's guided form.
type Param struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // bool, text, number, multi
	Hint     string   `json:"hint,omitempty"`
	Default  string   `json:"default"`
	Options  []Option `json:"options,omitempty"` // for multi
	Advanced bool     `json:"advanced,omitempty"`
	Min, Max int      `json:"-"`
}

// Def describes an overlay for the UI.
type Def struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Summary string  `json:"summary"` // what it does, in plain words
	Why     string  `json:"why"`     // when you'd want it
	Warning string  `json:"warning,omitempty"`
	Params  []Param `json:"params"`

	module, class string // internal: module name and config objectClass
}

// Context holds facts about this deployment that shape defaults and plans.
type Context struct {
	Suffix        string
	UsersBase     string
	GroupsBase    string
	GroupClass    string
	MemberAttr    string
	PlaceholderDN string // the DN Dapper uses to keep a group non-empty
}

// Overlay is an overlay already configured on the server.
type Overlay struct {
	DN     string
	Values map[string][]string
}

// State is what is currently configured on the server.
type State struct {
	DBDN     string // the database's config entry, e.g. olcDatabase={1}mdb,cn=config
	Suffix   string
	Modules  []string
	ModuleDN string
	Overlays map[string]*Overlay // by overlay id
}

// Op is one directory change.
type Op struct {
	Target string // "config" (cn=config) or "data" (the directory itself)
	Kind   string // add, modify, delete
	DN     string
	Attrs  map[string][]string // add
	Mods   []Mod               // modify
}

type Mod struct {
	Op     string // add, replace, delete
	Attr   string
	Values []string
}

type Plan struct {
	Summary  []string `json:"summary"`
	Warnings []string `json:"warnings"`
	Ops      []Op     `json:"-"`
}

// Facts are things only the live directory can tell us, needed to plan password-policy changes.
type Facts struct {
	PolicyOUExists bool
	PolicyExists   bool
	SchemaHas      map[string]bool // lower-cased object class names, nil if unknown
}

func catalog(ctx Context) []Def {
	groupOC, memberAD := ctx.GroupClass, ctx.MemberAttr
	if groupOC == "" {
		groupOC = "groupOfNames"
	}
	if memberAD == "" {
		memberAD = "member"
	}
	return []Def{
		{
			ID: "memberof", Name: "Group membership on each person (memberOf)", module: "memberof", class: "olcMemberOf",
			Summary: "Every person's entry lists the groups they belong to, kept up to date automatically.",
			Why:     "Many applications (GitLab, Jenkins, Grafana, VPNs and more) check group membership by reading it from the person's own entry.",
			Warning: "People who are already in groups won't show memberOf until their groups are refreshed. Dapper can do that for you after you turn this on.",
			Params: []Param{
				{Key: "refInt", Label: "Keep memberOf tidy when people or groups are removed", Type: "bool", Default: "true"},
				{Key: "groupOC", Label: "Group object class", Type: "text", Default: groupOC, Advanced: true},
				{Key: "memberAD", Label: "Group member attribute", Type: "text", Default: memberAD, Advanced: true},
			},
		},
		{
			ID: "refint", Name: "Keep group membership correct (referential integrity)", module: "refint", class: "olcRefintConfig",
			Summary: "When someone is renamed, moved or deleted, every group that mentions them is updated automatically.",
			Why:     "Without it, groups can be left pointing at people who no longer exist, and other systems that look people up by name can break.",
			Params: []Param{
				{Key: "attributes", Label: "Attributes to keep correct", Type: "text", Default: strings.TrimSpace(memberAD + " memberOf"),
					Hint: "Separate with spaces. Usually member and memberOf (add owner or manager if you use them)."},
			},
		},
		{
			ID: "ppolicy", Name: "Password rules and lockout (ppolicy)", module: "ppolicy", class: "olcPPolicyConfig",
			Summary: "Enforces password length, expiry, history and lockout after repeated wrong passwords, for everyone in the directory.",
			Why:     "Gives you real password security that applies to every application using this directory, and lets accounts be locked in place.",
			Warning: "Lockout applies to every account, including any service accounts your applications use. Check that Dapper's own service account and your other applications' accounts won't be locked by mistake (the directory's root account is never locked).",
			Params: []Param{
				{Key: "minLength", Label: "Minimum password length", Type: "number", Default: "8", Min: 1, Max: 128},
				{Key: "maxAgeDays", Label: "Passwords expire after (days, 0 = never)", Type: "number", Default: "0", Min: 0, Max: 3650},
				{Key: "history", Label: "Don't allow reusing the last N passwords (0 = off)", Type: "number", Default: "0", Min: 0, Max: 24},
				{Key: "lockout", Label: "Lock an account after repeated wrong passwords", Type: "bool", Default: "true"},
				{Key: "maxFailures", Label: "Wrong passwords allowed before locking", Type: "number", Default: "5", Min: 1, Max: 100},
				{Key: "lockoutMinutes", Label: "Locked for (minutes, 0 = until an administrator unlocks)", Type: "number", Default: "15", Min: 0, Max: 100000},
				{Key: "intervalMinutes", Label: "Forget earlier wrong passwords after (minutes)", Type: "number", Default: "15", Min: 1, Max: 100000, Advanced: true},
				{Key: "mustChange", Label: "Require a new password after an administrator resets it", Type: "bool", Default: "true", Advanced: true},
				{Key: "policyDN", Label: "Where the policy is stored", Type: "text", Default: "cn=default,ou=policies," + ctx.Suffix, Advanced: true,
					Hint: "The policy is an ordinary entry in your directory. Dapper creates it (and its ou=policies container) if missing."},
			},
		},
		{
			ID: "unique", Name: "Prevent duplicates (unique)", module: "unique", class: "olcUniqueConfig",
			Summary: "The directory refuses to save a second person with the same username, email or user ID.",
			Why:     "Duplicate usernames or user IDs cause confusing and sometimes dangerous problems on Linux systems.",
			Warning: "This only stops new duplicates. People who already share a value are not changed (Dapper lists any it finds before you turn this on).",
			Params: []Param{
				{Key: "attrs", Label: "Make these unique", Type: "multi", Default: "uid,uidNumber", Options: []Option{
					{Value: "uid", Label: "Username (uid)"}, {Value: "uidNumber", Label: "User ID number (uidNumber)"}, {Value: "mail", Label: "Email address (mail)"}}},
			},
		},
		{
			ID: "auditlog", Name: "Change log (auditlog)", module: "auditlog", class: "olcAuditlogConfig",
			Summary: "Records every change to the directory in a file on the directory server, so you can see who changed what.",
			Why:     "Useful for security reviews, and for working out what happened when something changes unexpectedly.",
			Warning: "The log can contain sensitive values such as password hashes. Keep the file private and rotate it. The file is written on the directory server, not by Dapper.",
			Params: []Param{
				{Key: "file", Label: "Log file on the directory server", Type: "text", Default: "/var/lib/ldap/auditlog.ldif",
					Hint: "The server's slapd user must be allowed to write here."},
			},
		},
	}
}

// Catalog returns the overlays Dapper can manage, with defaults for this deployment.
func Catalog(ctx Context) []Def { return catalog(ctx) }

func find(ctx Context, id string) (Def, bool) {
	for _, d := range catalog(ctx) {
		if d.ID == id {
			return d, true
		}
	}
	return Def{}, false
}

// ---- reading what's configured ----

// Active reports whether an overlay that exists on the server is actually in use. OpenLDAP 2.4 can't
// remove an overlay while it runs, so "off" means its key setting has been cleared; the overlay entry
// stays but does nothing.
func Active(id string, o *Overlay) bool {
	if o == nil {
		return false
	}
	switch id {
	case "ppolicy":
		return first(attr(o, "olcPPolicyDefault")) != ""
	case "unique":
		return len(attr(o, "olcUniqueURI")) > 0
	case "auditlog":
		f := first(attr(o, "olcAuditlogFile"))
		return f != "" && f != "/dev/null"
	case "refint":
		return len(attr(o, "olcRefintAttribute")) > 0
	}
	return true // memberOf has no off switch short of removing it
}

func first(v []string) string {
	if len(v) > 0 {
		return v[0]
	}
	return ""
}

func attr(o *Overlay, name string) []string {
	for k, v := range o.Values {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

var indexRE = regexp.MustCompile(`^\{\d+\}`)

// OverlayID turns an olcOverlay value such as "{0}memberof" into "memberof".
func OverlayID(v string) string {
	return strings.ToLower(indexRE.ReplaceAllString(strings.TrimSpace(v), ""))
}

// ModuleName normalises a module list value ("{1}memberof", "memberof.la") to "memberof".
func ModuleName(v string) string {
	v = indexRE.ReplaceAllString(strings.TrimSpace(v), "")
	for _, suf := range []string{".la", ".so"} {
		v = strings.TrimSuffix(v, suf)
	}
	return strings.ToLower(v)
}

var uriAttrRE = regexp.MustCompile(`^ldap:///[^?]*\?([^?]*)\?`)

// Values returns an overlay's settings in the same form the guided form uses.
func Values(id string, o *Overlay, policy map[string][]string) map[string]string {
	v := map[string]string{}
	if o == nil {
		return v
	}
	switch id {
	case "memberof":
		v["groupOC"], v["memberAD"] = first(attr(o, "olcMemberOfGroupOC")), first(attr(o, "olcMemberOfMemberAD"))
		v["refInt"] = boolStr(strings.EqualFold(first(attr(o, "olcMemberOfRefInt")), "TRUE"))
	case "refint":
		v["attributes"] = strings.Join(attr(o, "olcRefintAttribute"), " ")
	case "ppolicy":
		v["policyDN"] = first(attr(o, "olcPPolicyDefault"))
		if policy != nil {
			num := func(k string, div int) string {
				if x := first(policy[k]); x != "" {
					if n, err := strconv.Atoi(x); err == nil {
						return strconv.Itoa(n / div)
					}
				}
				return "0"
			}
			v["minLength"], v["maxAgeDays"], v["history"] = num("pwdMinLength", 1), num("pwdMaxAge", 86400), num("pwdInHistory", 1)
			v["lockout"] = boolStr(strings.EqualFold(first(policy["pwdLockout"]), "TRUE"))
			v["maxFailures"], v["lockoutMinutes"], v["intervalMinutes"] = num("pwdMaxFailure", 1), num("pwdLockoutDuration", 60), num("pwdFailureCountInterval", 60)
			v["mustChange"] = boolStr(strings.EqualFold(first(policy["pwdMustChange"]), "TRUE"))
		}
	case "unique":
		var attrs []string
		for _, u := range attr(o, "olcUniqueURI") {
			if m := uriAttrRE.FindStringSubmatch(strings.TrimSpace(u)); m != nil {
				attrs = append(attrs, strings.Fields(m[1])...)
			}
		}
		v["attrs"] = strings.Join(attrs, ",")
	case "auditlog":
		v["file"] = first(attr(o, "olcAuditlogFile"))
	}
	return v
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ---- validation helpers ----

var attrNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

type inputError string

func (e inputError) Error() string { return string(e) }

func intVal(vals map[string]string, p Param) (int, error) {
	raw := strings.TrimSpace(vals[p.Key])
	if raw == "" {
		raw = p.Default
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < p.Min || n > p.Max {
		return 0, inputError(fmt.Sprintf("“%s” must be a whole number from %d to %d.", p.Label, p.Min, p.Max))
	}
	return n, nil
}

func str(vals map[string]string, key, def string) string {
	if v := strings.TrimSpace(vals[key]); v != "" {
		return v
	}
	return def
}

func truthy(vals map[string]string, key string, def bool) bool {
	v, ok := vals[key]
	if !ok || v == "" {
		return def
	}
	return strings.EqualFold(v, "true")
}

func validDN(dn string) bool { _, err := ldap.ParseDN(dn); return err == nil && dn != "" }

// ---- planning ----

// BuildPlan works out the changes for turning an overlay on, changing it or turning it off.
// action is enable, update or disable.
func BuildPlan(ctx Context, st *State, id, action string, vals map[string]string, facts Facts) (*Plan, error) {
	def, ok := find(ctx, id)
	if !ok {
		return nil, inputError("Unknown overlay.")
	}
	if st.DBDN == "" {
		return nil, inputError("The directory's database couldn't be identified.")
	}
	cur := st.Overlays[id]
	p := &Plan{}
	active := Active(id, cur)

	switch action {
	case "enable", "update":
		if action == "enable" && active {
			return nil, inputError(def.Name + " is already turned on. Use Change instead.")
		}
		if action == "update" && cur == nil {
			return nil, inputError(def.Name + " isn't turned on yet.")
		}
		if action == "enable" && cur != nil {
			action = "update" // it exists but was turned off: switch it back on in place
		}
	case "disable":
		if !active {
			return nil, inputError(def.Name + " isn't turned on.")
		}
		return planDisable(def, cur, p)
	default:
		return nil, inputError("Unknown action.")
	}

	attrs, mods, err := buildAttrs(ctx, def, vals, p, facts)
	if err != nil {
		return nil, err
	}

	if action == "enable" && cur == nil {
		p.Ops = append(p.Ops, moduleOps(st, def.module, p)...)
		attrs["objectClass"] = []string{"olcOverlayConfig", def.class}
		attrs["olcOverlay"] = []string{def.module}
		if id == "auditlog" || id == "ppolicy" || id == "unique" || id == "refint" || id == "memberof" {
			attrs["olcOverlay"] = []string{overlayName(id)}
		}
		// Data changes (the password policy entry) come after the server knows the overlay.
		p.Ops = append(p.Ops, Op{Target: "config", Kind: "add", DN: "olcOverlay=" + overlayName(id) + "," + st.DBDN, Attrs: attrs})
		p.Summary = append([]string{"Turn on “" + def.Name + "”: " + lowerFirst(def.Summary)}, p.Summary...)
		if def.Warning != "" {
			p.Warnings = append(p.Warnings, def.Warning)
		}
	} else {
		p.Ops = append(p.Ops, Op{Target: "config", Kind: "modify", DN: cur.DN, Mods: mods})
		lead := "Change the settings of “" + def.Name + "”."
		if !Active(id, cur) {
			lead = "Turn “" + def.Name + "” back on with these settings."
			if def.Warning != "" {
				p.Warnings = append(p.Warnings, def.Warning)
			}
		}
		p.Summary = append([]string{lead}, p.Summary...)
	}
	// Directory data (the password policy entry) goes first, then the server configuration that
	// starts using it: if a step fails, the worst leftover is an unused entry, never a server
	// enforcing a policy that doesn't exist.
	sort.SliceStable(p.Ops, func(i, j int) bool { return p.Ops[i].Target == "data" && p.Ops[j].Target == "config" })
	return p, nil
}

func overlayName(id string) string { return id } // olcOverlay values match the ids

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// planDisable turns an overlay off by clearing its key setting. Removing the overlay entry would be
// the tidy way, but OpenLDAP 2.4 refuses to remove one while the server runs, and clearing the setting
// works on every version.
func planDisable(def Def, cur *Overlay, p *Plan) (*Plan, error) {
	var mods []Mod
	switch def.ID {
	case "ppolicy":
		mods = []Mod{{Op: "delete", Attr: "olcPPolicyDefault"}}
		p.Warnings = append(p.Warnings, "Password rules and lockout stop being enforced. Accounts that are already locked stay locked until an administrator unlocks them. The policy entry in your directory is left in place.")
	case "unique":
		mods = []Mod{{Op: "delete", Attr: "olcUniqueURI"}}
		p.Warnings = append(p.Warnings, "The directory will accept duplicate usernames, emails and IDs again.")
	case "auditlog":
		mods = []Mod{{Op: "replace", Attr: "olcAuditlogFile", Values: []string{"/dev/null"}}}
		p.Warnings = append(p.Warnings, "Changes stop being recorded. The existing log file is left where it is.")
	case "refint":
		mods = []Mod{{Op: "delete", Attr: "olcRefintAttribute"}}
		p.Warnings = append(p.Warnings, "Groups will no longer be updated automatically when people are renamed, moved or deleted.")
	default:
		return nil, inputError("“" + def.Name + "” can't be switched off while the directory server is running. To remove it, edit the server's configuration and restart it, or leave it on: it only adds a memberOf list to people's entries.")
	}
	p.Ops = append(p.Ops, Op{Target: "config", Kind: "modify", DN: cur.DN, Mods: mods})
	p.Summary = append(p.Summary, "Turn off “"+def.Name+"”. Its settings are cleared so it does nothing; the module stays loaded on the server (OpenLDAP can't unload it while running).")
	return p, nil
}

// moduleOps loads the overlay's module if the server hasn't already.
func moduleOps(st *State, module string, p *Plan) []Op {
	for _, m := range st.Modules {
		if ModuleName(m) == module {
			return nil
		}
	}
	style := ""
	for _, m := range st.Modules {
		if strings.HasSuffix(m, ".la") {
			style = ".la"
		}
	}
	p.Summary = append(p.Summary, "Load the “"+module+"” module on the directory server.")
	if st.ModuleDN != "" {
		return []Op{{Target: "config", Kind: "modify", DN: st.ModuleDN, Mods: []Mod{{Op: "add", Attr: "olcModuleLoad", Values: []string{module + style}}}}}
	}
	return []Op{{Target: "config", Kind: "add", DN: "cn=module{0},cn=config", Attrs: map[string][]string{
		"objectClass": {"olcModuleList"}, "cn": {"module{0}"}, "olcModuleLoad": {module + style},
	}}}
}

// buildAttrs validates the form and returns the overlay's attributes (for adding) and modifications (for changing).
func buildAttrs(ctx Context, def Def, vals map[string]string, p *Plan, facts Facts) (map[string][]string, []Mod, error) {
	attrs := map[string][]string{}
	var mods []Mod
	set := func(name string, v ...string) {
		attrs[name] = v
		mods = append(mods, Mod{Op: "replace", Attr: name, Values: v})
	}
	param := func(key string) Param {
		for _, q := range def.Params {
			if q.Key == key {
				return q
			}
		}
		return Param{Key: key}
	}

	switch def.ID {
	case "memberof":
		oc, ad := str(vals, "groupOC", param("groupOC").Default), str(vals, "memberAD", param("memberAD").Default)
		for _, n := range []string{oc, ad} {
			if !attrNameRE.MatchString(n) {
				return nil, nil, inputError("“" + n + "” isn't a valid attribute or class name.")
			}
		}
		ref := truthy(vals, "refInt", true)
		set("olcMemberOfGroupOC", oc)
		set("olcMemberOfMemberAD", ad)
		set("olcMemberOfMemberOfAD", "memberOf")
		set("olcMemberOfRefInt", strings.ToUpper(boolStr(ref)))
		set("olcMemberOfDangling", "ignore")
		p.Summary = append(p.Summary, fmt.Sprintf("Groups of type %s will be tracked through their %s attribute.", oc, ad))
	case "refint":
		fields := strings.Fields(str(vals, "attributes", param("attributes").Default))
		if len(fields) == 0 {
			return nil, nil, inputError("Enter at least one attribute to keep correct.")
		}
		for _, f := range fields {
			if !attrNameRE.MatchString(f) {
				return nil, nil, inputError("“" + f + "” isn't a valid attribute name.")
			}
		}
		set("olcRefintAttribute", strings.Join(fields, " "))
		if ctx.PlaceholderDN != "" {
			// When removing someone would empty a group, put Dapper's placeholder in so the group stays valid.
			set("olcRefintNothing", ctx.PlaceholderDN)
		}
		p.Summary = append(p.Summary, "Keep these attributes correct automatically: "+strings.Join(fields, ", ")+".")
	case "unique":
		var chosen []string
		for _, a := range strings.Split(vals["attrs"], ",") {
			if a = strings.TrimSpace(a); a != "" {
				ok := false
				for _, o := range param("attrs").Options {
					ok = ok || o.Value == a
				}
				if !ok {
					return nil, nil, inputError("“" + a + "” isn't one of the choices.")
				}
				chosen = append(chosen, a)
			}
		}
		if len(chosen) == 0 {
			return nil, nil, inputError("Choose at least one thing to keep unique.")
		}
		var uris []string
		for _, a := range chosen {
			uris = append(uris, "ldap:///"+ctx.UsersBase+"?"+a+"?sub")
		}
		set("olcUniqueURI", uris...)
		p.Summary = append(p.Summary, "No two people under "+ctx.UsersBase+" may share the same: "+strings.Join(chosen, ", ")+".")
	case "auditlog":
		f := str(vals, "file", param("file").Default)
		if !strings.HasPrefix(f, "/") || strings.ContainsAny(f, " \t\n\r") || strings.Contains(f, "..") {
			return nil, nil, inputError("The log file must be an absolute path without spaces, such as /var/lib/ldap/auditlog.ldif.")
		}
		set("olcAuditlogFile", f)
		p.Summary = append(p.Summary, "Write every change to "+f+" on the directory server.")
	case "ppolicy":
		if err := policyOps(ctx, def, vals, p, facts, set); err != nil {
			return nil, nil, err
		}
	}
	return attrs, mods, nil
}

func policyOps(ctx Context, def Def, vals map[string]string, p *Plan, facts Facts, set func(string, ...string)) error {
	get := func(key string) (int, error) {
		for _, q := range def.Params {
			if q.Key == key {
				return intVal(vals, q)
			}
		}
		return 0, nil
	}
	pdn := str(vals, "policyDN", "cn=default,ou=policies,"+ctx.Suffix)
	if !validDN(pdn) {
		return inputError("The policy location isn't a valid directory name.")
	}
	n := map[string]int{}
	for _, k := range []string{"minLength", "maxAgeDays", "history", "maxFailures", "lockoutMinutes", "intervalMinutes"} {
		v, err := get(k)
		if err != nil {
			return err
		}
		n[k] = v
	}
	lockout, mustChange := truthy(vals, "lockout", true), truthy(vals, "mustChange", true)
	if facts.SchemaHas != nil && !facts.SchemaHas["pwdpolicy"] {
		return inputError("This server's schema doesn't include the password policy classes (pwdPolicy). Load the ppolicy schema first. On most systems: sudo ldapadd -Y EXTERNAL -H ldapi:/// -f /etc/ldap/schema/ppolicy.ldif")
	}

	set("olcPPolicyDefault", pdn)
	set("olcPPolicyHashCleartext", "TRUE")
	set("olcPPolicyUseLockout", "FALSE")

	// The policy itself is an ordinary entry in the directory.
	parent := parentDN(pdn)
	if parent != "" && !facts.PolicyOUExists {
		a, v := rdn(parent)
		if !strings.EqualFold(a, "ou") {
			return inputError("The policy's container " + parent + " doesn't exist. Create it first, or choose a location under an ou= container.")
		}
		p.Ops = append(p.Ops, Op{Target: "data", Kind: "add", DN: parent, Attrs: map[string][]string{"objectClass": {"organizationalUnit"}, "ou": {v}}})
		p.Summary = append(p.Summary, "Create the container "+parent+" for password policies.")
	}
	pol := map[string][]string{
		"pwdAttribute":       {"2.5.4.35"}, // userPassword, by OID: some servers reject the name
		"pwdMinLength":       {strconv.Itoa(n["minLength"])},
		"pwdLockout":         {strings.ToUpper(boolStr(lockout))},
		"pwdMustChange":      {strings.ToUpper(boolStr(mustChange))},
		"pwdAllowUserChange": {"TRUE"},
		"pwdSafeModify":      {"FALSE"},
	}
	if n["maxAgeDays"] > 0 {
		pol["pwdMaxAge"] = []string{strconv.Itoa(n["maxAgeDays"] * 86400)}
		pol["pwdExpireWarning"] = []string{"604800"}
	}
	if n["history"] > 0 {
		pol["pwdInHistory"] = []string{strconv.Itoa(n["history"])}
	}
	if lockout {
		pol["pwdMaxFailure"] = []string{strconv.Itoa(n["maxFailures"])}
		pol["pwdLockoutDuration"] = []string{strconv.Itoa(n["lockoutMinutes"] * 60)}
		pol["pwdFailureCountInterval"] = []string{strconv.Itoa(n["intervalMinutes"] * 60)}
	}
	if facts.PolicyExists {
		var mods []Mod
		for _, a := range []string{"pwdAttribute", "pwdMinLength", "pwdLockout", "pwdMustChange", "pwdAllowUserChange", "pwdSafeModify",
			"pwdMaxAge", "pwdExpireWarning", "pwdInHistory", "pwdMaxFailure", "pwdLockoutDuration", "pwdFailureCountInterval"} {
			if v, ok := pol[a]; ok {
				mods = append(mods, Mod{Op: "replace", Attr: a, Values: v})
			} else {
				// Switched off: "replace with nothing" removes the attribute, and unlike "delete" it
				// isn't an error when the attribute isn't there.
				mods = append(mods, Mod{Op: "replace", Attr: a})
			}
		}
		p.Ops = append(p.Ops, Op{Target: "data", Kind: "modify", DN: pdn, Mods: mods})
		p.Summary = append(p.Summary, "Update the password policy entry "+pdn+".")
	} else {
		_, cn := rdn(pdn)
		pol["objectClass"] = []string{"device", "pwdPolicy"}
		pol["cn"] = []string{cn}
		p.Ops = append(p.Ops, Op{Target: "data", Kind: "add", DN: pdn, Attrs: pol})
		p.Summary = append(p.Summary, "Create the password policy entry "+pdn+".")
	}
	rules := []string{fmt.Sprintf("at least %d characters", n["minLength"])}
	if n["maxAgeDays"] > 0 {
		rules = append(rules, fmt.Sprintf("expire after %d days", n["maxAgeDays"]))
	}
	if n["history"] > 0 {
		rules = append(rules, fmt.Sprintf("can't reuse the last %d passwords", n["history"]))
	}
	if lockout {
		d := "until an administrator unlocks the account"
		if n["lockoutMinutes"] > 0 {
			d = fmt.Sprintf("for %d minutes", n["lockoutMinutes"])
		}
		rules = append(rules, fmt.Sprintf("account locked %s after %d wrong passwords", d, n["maxFailures"]))
	}
	p.Summary = append(p.Summary, "Passwords: "+strings.Join(rules, "; ")+".")
	return nil
}

func parentDN(dn string) string {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) < 2 {
		return ""
	}
	parts := make([]string, 0, len(d.RDNs)-1)
	for _, r := range d.RDNs[1:] {
		var as []string
		for _, a := range r.Attributes {
			as = append(as, a.Type+"="+ldapEscapeDN(a.Value))
		}
		parts = append(parts, strings.Join(as, "+"))
	}
	return strings.Join(parts, ",")
}

func rdn(dn string) (attr, value string) {
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) == 0 || len(d.RDNs[0].Attributes) == 0 {
		return "", ""
	}
	return d.RDNs[0].Attributes[0].Type, d.RDNs[0].Attributes[0].Value
}

func ldapEscapeDN(v string) string {
	var b strings.Builder
	for i, r := range v {
		if strings.ContainsRune(`,+"\<>;=`, r) || (i == 0 && (r == '#' || r == ' ')) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ---- LDIF ----

func ldifLine(name, value string) string {
	safe := value != ""
	for i, r := range value {
		if r > unicode.MaxASCII || r < 0x20 || r == 0x7f || (i == 0 && (r == ' ' || r == ':' || r == '<')) {
			safe = false
		}
	}
	if strings.HasSuffix(value, " ") {
		safe = false
	}
	if !safe {
		return name + ":: " + base64.StdEncoding.EncodeToString([]byte(value))
	}
	return name + ": " + value
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		// objectClass first, then alphabetical
		if strings.EqualFold(keys[i], "objectClass") != strings.EqualFold(keys[j], "objectClass") {
			return strings.EqualFold(keys[i], "objectClass")
		}
		return keys[i] < keys[j]
	})
	return keys
}

// LDIF renders the operations for one target ("config" or "data") as LDIF that
// ldapmodify can apply.
func (p *Plan) LDIF(target string) string {
	var b strings.Builder
	for _, op := range p.Ops {
		if op.Target != target {
			continue
		}
		b.WriteString(ldifLine("dn", op.DN) + "\n")
		switch op.Kind {
		case "add":
			b.WriteString("changetype: add\n")
			for _, k := range sortedKeys(op.Attrs) {
				for _, v := range op.Attrs[k] {
					b.WriteString(ldifLine(k, v) + "\n")
				}
			}
		case "modify":
			b.WriteString("changetype: modify\n")
			for i, m := range op.Mods {
				if i > 0 {
					b.WriteString("-\n")
				}
				b.WriteString(m.Op + ": " + m.Attr + "\n")
				for _, v := range m.Values {
					b.WriteString(ldifLine(m.Attr, v) + "\n")
				}
			}
		case "delete":
			b.WriteString("changetype: delete\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// HasOps reports whether the plan changes the given target.
func (p *Plan) HasOps(target string) bool {
	for _, op := range p.Ops {
		if op.Target == target {
			return true
		}
	}
	return false
}

// IsInputError reports whether err is a problem with what the administrator entered.
func IsInputError(err error) bool { _, ok := err.(inputError); return ok }

// Defs returns catalog entries with defaults filled in.
func Defs(ctx Context) []Def { return catalog(ctx) }
