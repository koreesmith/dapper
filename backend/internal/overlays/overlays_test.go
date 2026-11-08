package overlays

import (
	"strings"
	"testing"
)

var ctx = Context{
	Suffix: "dc=example,dc=org", UsersBase: "ou=people,dc=example,dc=org", GroupsBase: "ou=groups,dc=example,dc=org",
	GroupClass: "groupOfNames", MemberAttr: "member", PlaceholderDN: "cn=dapper-placeholder,ou=groups,dc=example,dc=org",
}

func emptyState() *State {
	return &State{DBDN: "olcDatabase={1}mdb,cn=config", Suffix: ctx.Suffix, Overlays: map[string]*Overlay{}}
}

func TestModuleAndOverlayNames(t *testing.T) {
	for in, want := range map[string]string{"{1}memberof": "memberof", "memberof.la": "memberof", "{0}ppolicy.so": "ppolicy", "Unique": "unique"} {
		if got := ModuleName(in); got != want {
			t.Errorf("ModuleName(%q) = %q, want %q", in, got, want)
		}
	}
	if OverlayID("{0}memberof") != "memberof" || OverlayID(" {12}RefInt ") != "refint" {
		t.Error("OverlayID")
	}
}

func TestEnableLoadsModuleOnlyWhenNeeded(t *testing.T) {
	st := emptyState()
	st.ModuleDN, st.Modules = "cn=module{0},cn=config", []string{"{0}back_mdb"}
	p, err := BuildPlan(ctx, st, "memberof", "enable", map[string]string{}, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	ldif := p.LDIF("config")
	for _, want := range []string{
		"dn: cn=module{0},cn=config\nchangetype: modify\nadd: olcModuleLoad\nolcModuleLoad: memberof\n",
		"dn: olcOverlay=memberof,olcDatabase={1}mdb,cn=config\nchangetype: add\nobjectClass: olcOverlayConfig\nobjectClass: olcMemberOf\n",
		"olcMemberOfGroupOC: groupOfNames", "olcMemberOfMemberAD: member", "olcMemberOfMemberOfAD: memberOf", "olcMemberOfRefInt: TRUE",
	} {
		if !strings.Contains(ldif, want) {
			t.Errorf("missing %q in:\n%s", want, ldif)
		}
	}
	// Module already loaded (in any spelling): no module change.
	st.Modules = []string{"{0}back_mdb", "{1}memberof.la"}
	p, _ = BuildPlan(ctx, st, "memberof", "enable", map[string]string{}, Facts{})
	if strings.Contains(p.LDIF("config"), "olcModuleLoad") {
		t.Errorf("module is already loaded:\n%s", p.LDIF("config"))
	}
	// No module list at all (static build or first overlay): create one.
	st2 := emptyState()
	p, _ = BuildPlan(ctx, st2, "unique", "enable", map[string]string{"attrs": "uid"}, Facts{})
	if !strings.Contains(p.LDIF("config"), "dn: cn=module{0},cn=config\nchangetype: add\nobjectClass: olcModuleList") {
		t.Errorf("should create a module list:\n%s", p.LDIF("config"))
	}
	// The style of existing entries (.la) is followed.
	st.Modules = []string{"{0}back_mdb.la"}
	p, _ = BuildPlan(ctx, st, "refint", "enable", map[string]string{}, Facts{})
	if !strings.Contains(p.LDIF("config"), "olcModuleLoad: refint.la") {
		t.Errorf("should follow .la style:\n%s", p.LDIF("config"))
	}
}

func TestEnableRules(t *testing.T) {
	st := emptyState()
	st.Overlays["memberof"] = &Overlay{DN: "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config"}
	if _, err := BuildPlan(ctx, st, "memberof", "enable", nil, Facts{}); err == nil {
		t.Error("enabling twice must be refused")
	}
	if _, err := BuildPlan(ctx, st, "unique", "update", map[string]string{"attrs": "uid"}, Facts{}); err == nil {
		t.Error("updating something that isn't on must be refused")
	}
	if _, err := BuildPlan(ctx, st, "unique", "disable", nil, Facts{}); err == nil {
		t.Error("disabling something that isn't on must be refused")
	}
	if _, err := BuildPlan(ctx, st, "nonsense", "enable", nil, Facts{}); err == nil {
		t.Error("unknown overlay")
	}
	if _, err := BuildPlan(ctx, &State{}, "memberof", "enable", nil, Facts{}); err == nil {
		t.Error("no database found")
	}
}

func TestTurningOffClearsTheKeySetting(t *testing.T) {
	dn := "olcOverlay={4}ppolicy,olcDatabase={1}mdb,cn=config"
	cases := map[string]struct {
		attrs map[string][]string
		want  string
	}{
		"ppolicy":  {map[string][]string{"olcPPolicyDefault": {"cn=default,ou=p,dc=x"}}, "delete: olcPPolicyDefault\n"},
		"unique":   {map[string][]string{"olcUniqueURI": {"ldap:///ou=p,dc=x?uid?sub"}}, "delete: olcUniqueURI\n"},
		"auditlog": {map[string][]string{"olcAuditlogFile": {"/var/log/a.ldif"}}, "replace: olcAuditlogFile\nolcAuditlogFile: /dev/null\n"},
		"refint":   {map[string][]string{"olcRefintAttribute": {"member"}}, "delete: olcRefintAttribute\n"},
	}
	for id, c := range cases {
		st := emptyState()
		st.Overlays[id] = &Overlay{DN: dn, Values: c.attrs}
		p, err := BuildPlan(ctx, st, id, "disable", nil, Facts{})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		l := p.LDIF("config")
		if !strings.Contains(l, "dn: "+dn+"\nchangetype: modify\n"+c.want) || strings.Contains(l, "changetype: delete") {
			t.Errorf("%s ldif:\n%s", id, l)
		}
		if len(p.Warnings) == 0 {
			t.Errorf("%s: turning off should warn", id)
		}
	}
	// memberOf has no off switch: explain instead of pretending.
	st := emptyState()
	st.Overlays["memberof"] = &Overlay{DN: "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config"}
	if _, err := BuildPlan(ctx, st, "memberof", "disable", nil, Facts{}); err == nil || !IsInputError(err) || !strings.Contains(err.Error(), "restart") {
		t.Errorf("memberof disable: %v", err)
	}
}

func TestActiveAndTurningBackOn(t *testing.T) {
	dn := "olcOverlay={2}unique,olcDatabase={1}mdb,cn=config"
	off := &Overlay{DN: dn, Values: map[string][]string{"objectClass": {"olcUniqueConfig"}}}
	if Active("unique", off) || Active("unique", nil) {
		t.Error("an overlay with no URIs is off")
	}
	if !Active("unique", &Overlay{Values: map[string][]string{"olcUniqueURI": {"ldap:///x?uid?sub"}}}) || Active("auditlog", &Overlay{Values: map[string][]string{"olcAuditlogFile": {"/dev/null"}}}) {
		t.Error("Active")
	}
	st := emptyState()
	st.Modules = []string{"unique"}
	st.Overlays["unique"] = off
	// Enabling something that exists but is off modifies it in place (no new entry, no module change).
	p, err := BuildPlan(ctx, st, "unique", "enable", map[string]string{"attrs": "uid"}, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	l := p.LDIF("config")
	if strings.Contains(l, "changetype: add") || strings.Contains(l, "olcModuleLoad") || !strings.Contains(l, "replace: olcUniqueURI") {
		t.Errorf("turn back on:\n%s", l)
	}
	if _, err := BuildPlan(ctx, st, "unique", "disable", nil, Facts{}); err == nil {
		t.Error("turning off something already off must be refused")
	}
}

func TestValidation(t *testing.T) {
	st := emptyState()
	bad := []struct {
		id   string
		vals map[string]string
	}{
		{"memberof", map[string]string{"groupOC": "bad class!"}},
		{"refint", map[string]string{"attributes": "member; drop"}},
		{"unique", map[string]string{"attrs": "userPassword"}},
		{"unique", map[string]string{"attrs": ""}},
		{"auditlog", map[string]string{"file": "relative.ldif"}},
		{"auditlog", map[string]string{"file": "/var/log/../etc/passwd"}},
		{"auditlog", map[string]string{"file": "/var/log/a b.ldif"}},
		{"ppolicy", map[string]string{"minLength": "0"}},
		{"ppolicy", map[string]string{"minLength": "abc"}},
		{"ppolicy", map[string]string{"maxFailures": "1000"}},
		{"ppolicy", map[string]string{"policyDN": "not a dn"}},
	}
	for _, b := range bad {
		if _, err := BuildPlan(ctx, st, b.id, "enable", b.vals, Facts{PolicyOUExists: true}); err == nil {
			t.Errorf("%s %v should be rejected", b.id, b.vals)
		} else if !IsInputError(err) {
			t.Errorf("%s: should be an input error, got %T", b.id, err)
		}
	}
}

func TestRefintUsesDapperPlaceholder(t *testing.T) {
	p, err := BuildPlan(ctx, emptyState(), "refint", "enable", map[string]string{}, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	l := p.LDIF("config")
	if !strings.Contains(l, "olcRefintAttribute: member memberOf") || !strings.Contains(l, "olcRefintNothing: cn=dapper-placeholder,ou=groups,dc=example,dc=org") {
		t.Errorf("refint ldif:\n%s", l)
	}
}

func TestUniqueBuildsOneURIPerAttribute(t *testing.T) {
	p, err := BuildPlan(ctx, emptyState(), "unique", "enable", map[string]string{"attrs": "uid, mail"}, Facts{})
	if err != nil {
		t.Fatal(err)
	}
	l := p.LDIF("config")
	for _, w := range []string{"olcUniqueURI: ldap:///ou=people,dc=example,dc=org?uid?sub", "olcUniqueURI: ldap:///ou=people,dc=example,dc=org?mail?sub"} {
		if !strings.Contains(l, w) {
			t.Errorf("missing %q in\n%s", w, l)
		}
	}
	// Reading it back gives the same choices.
	o := &Overlay{Values: map[string][]string{"olcUniqueURI": {"ldap:///ou=people,dc=example,dc=org?uid?sub", "ldap:///ou=people,dc=example,dc=org?mail?sub"}}}
	if got := Values("unique", o, nil)["attrs"]; got != "uid,mail" {
		t.Errorf("round trip: %q", got)
	}
}

func TestPolicyCreatesContainerAndEntry(t *testing.T) {
	p, err := BuildPlan(ctx, emptyState(), "ppolicy", "enable", map[string]string{
		"minLength": "10", "maxAgeDays": "90", "history": "5", "lockout": "true", "maxFailures": "4", "lockoutMinutes": "30",
	}, Facts{SchemaHas: map[string]bool{"pwdpolicy": true}})
	if err != nil {
		t.Fatal(err)
	}
	data := p.LDIF("data")
	for _, w := range []string{
		"dn: ou=policies,dc=example,dc=org\nchangetype: add\nobjectClass: organizationalUnit",
		"dn: cn=default,ou=policies,dc=example,dc=org\nchangetype: add\nobjectClass: device\nobjectClass: pwdPolicy",
		"pwdMinLength: 10", "pwdMaxAge: 7776000", "pwdInHistory: 5", "pwdLockout: TRUE", "pwdMaxFailure: 4", "pwdLockoutDuration: 1800",
		"pwdAttribute: 2.5.4.35", "pwdMustChange: TRUE",
	} {
		if !strings.Contains(data, w) {
			t.Errorf("missing %q in data LDIF:\n%s", w, data)
		}
	}
	cfg := p.LDIF("config")
	if !strings.Contains(cfg, "olcPPolicyDefault: cn=default,ou=policies,dc=example,dc=org") || !strings.Contains(cfg, "olcPPolicyHashCleartext: TRUE") {
		t.Errorf("config ldif:\n%s", cfg)
	}
	// The policy entry is created before the overlay that uses it is switched on.
	last := p.Ops[len(p.Ops)-1]
	if p.Ops[0].Target != "data" || last.Target != "config" || !strings.HasPrefix(last.DN, "olcOverlay=ppolicy,") {
		t.Errorf("data ops must precede the overlay being enabled: %+v", p.Ops)
	}
	if len(p.Warnings) == 0 || !strings.Contains(strings.Join(p.Warnings, " "), "service accounts") {
		t.Error("should warn about lockout hitting service accounts")
	}
}

func TestPolicyUpdateAndSchemaGuard(t *testing.T) {
	// Existing policy: modify, removing what's switched off.
	p, err := BuildPlan(ctx, emptyState(), "ppolicy", "enable", map[string]string{"lockout": "false", "maxAgeDays": "0"},
		Facts{PolicyOUExists: true, PolicyExists: true})
	if err != nil {
		t.Fatal(err)
	}
	data := p.LDIF("data")
	if strings.Contains(data, "changetype: add") || strings.Contains(data, "delete: ") ||
		!strings.Contains(data, "replace: pwdMaxFailure\n-\n") || !strings.Contains(data, "replace: pwdMaxAge\n-\n") || !strings.Contains(data, "pwdLockout: FALSE") {
		t.Errorf("modify ldif:\n%s", data)
	}
	// Schema without pwdPolicy: refuse with instructions.
	_, err = BuildPlan(ctx, emptyState(), "ppolicy", "enable", map[string]string{}, Facts{SchemaHas: map[string]bool{"person": true}})
	if err == nil || !strings.Contains(err.Error(), "ppolicy.ldif") {
		t.Errorf("schema guard: %v", err)
	}
	// A policy outside any ou= container that doesn't exist can't be created automatically.
	_, err = BuildPlan(ctx, emptyState(), "ppolicy", "enable", map[string]string{"policyDN": "cn=default,cn=pol,dc=example,dc=org"}, Facts{})
	if err == nil {
		t.Error("should refuse to create a non-ou container")
	}
}

func TestValuesReadBack(t *testing.T) {
	m := &Overlay{Values: map[string][]string{"olcMemberOfGroupOC": {"groupOfNames"}, "olcMemberOfMemberAD": {"member"}, "olcMemberOfRefInt": {"TRUE"}}}
	v := Values("memberof", m, nil)
	if v["groupOC"] != "groupOfNames" || v["memberAD"] != "member" || v["refInt"] != "true" {
		t.Errorf("%v", v)
	}
	pv := Values("ppolicy", &Overlay{Values: map[string][]string{"olcPPolicyDefault": {"cn=default,ou=policies,dc=x"}}}, map[string][]string{
		"pwdMinLength": {"12"}, "pwdMaxAge": {"172800"}, "pwdLockout": {"TRUE"}, "pwdMaxFailure": {"3"}, "pwdLockoutDuration": {"600"},
	})
	if pv["minLength"] != "12" || pv["maxAgeDays"] != "2" || pv["lockout"] != "true" || pv["maxFailures"] != "3" || pv["lockoutMinutes"] != "10" {
		t.Errorf("%v", pv)
	}
	if got := Values("memberof", nil, nil); len(got) != 0 {
		t.Error("nil overlay has no values")
	}
}

func TestLDIFEncoding(t *testing.T) {
	if got := ldifLine("cn", "plain"); got != "cn: plain" {
		t.Error(got)
	}
	for _, v := range []string{" leading", "trailing ", ":colon", "<angle", "café", "two\nlines", ""} {
		if got := ldifLine("cn", v); !strings.HasPrefix(got, "cn:: ") {
			t.Errorf("%q should be base64: %s", v, got)
		}
	}
	p := &Plan{Ops: []Op{{Target: "config", Kind: "modify", DN: "cn=x", Mods: []Mod{
		{Op: "replace", Attr: "a", Values: []string{"1"}}, {Op: "delete", Attr: "b"}}}}}
	if got := p.LDIF("config"); got != "dn: cn=x\nchangetype: modify\nreplace: a\na: 1\n-\ndelete: b\n\n" {
		t.Errorf("modify ldif: %q", got)
	}
	if p.LDIF("data") != "" || p.HasOps("data") || !p.HasOps("config") {
		t.Error("target filtering")
	}
}
