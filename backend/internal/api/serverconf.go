package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/overlays"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// The rest of the Server page: what the directory server has loaded (schemas), how it is tuned (indexes),
// who may read and write what (access rules), its certificate (TLS), and how it copies itself (replication).
// Everything is read through the same configuration login as the overlays, and every change is
// previewed as LDIF, backed up, and approved before it runs.

type indexLine struct {
	Raw   string   `json:"raw"`
	Attrs []string `json:"attrs"`
	Types []string `json:"types"`
}

type certInfo struct {
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	NotBefore string   `json:"notBefore"`
	NotAfter  string   `json:"notAfter"`
	DaysLeft  int      `json:"daysLeft"`
	Expired   bool     `json:"expired"`
	Names     []string `json:"names"`
	SelfSign  bool     `json:"selfSigned"`
}

var (
	idxAttrRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]{0,63}$`)
	pathOKRE  = regexp.MustCompile(`^/[A-Za-z0-9._/@+-]{1,254}$`)
	seqPrefix = regexp.MustCompile(`^\{[0-9]+\}`)
)

func parseIndexes(vals []string) []indexLine {
	out := make([]indexLine, 0, len(vals))
	for _, v := range vals {
		f := strings.Fields(strings.TrimSpace(seqPrefix.ReplaceAllString(strings.TrimSpace(v), "")))
		if len(f) == 0 {
			continue
		}
		l := indexLine{Raw: strings.Join(f, " "), Attrs: strings.Split(f[0], ","), Types: []string{}}
		if len(f) > 1 {
			l.Types = strings.Split(f[1], ",")
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attrs[0] < out[j].Attrs[0] })
	return out
}

// servedCertificate connects to the directory server the way Dapper does and reads the certificate it presents,
// without trusting it (the point is to show what is there, including a bad one).
func servedCertificate(raw string, startTLS bool) (*certInfo, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the server address isn't a valid URL")
	}
	var state tls.ConnectionState
	cfg := &tls.Config{InsecureSkipVerify: true, ServerName: u.Hostname()} // #nosec G402 -- only reads the certificate, sends no credentials
	if u.Scheme == "ldaps" {
		host := u.Host
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "636")
		}
		c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", host, cfg)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		state = c.ConnectionState()
	} else {
		l, err := ldap.DialURL(raw, ldap.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}))
		if err != nil {
			return nil, err
		}
		defer l.Close()
		if err := l.StartTLS(cfg); err != nil {
			return nil, fmt.Errorf("the server doesn't offer encryption on this address (%v)", err)
		}
		var ok bool
		if state, ok = l.TLSConnectionState(); !ok {
			return nil, fmt.Errorf("no certificate was presented")
		}
	}
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no certificate was presented")
	}
	return describeCert(state.PeerCertificates[0]), nil
}

func describeCert(c *x509.Certificate) *certInfo {
	names := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	left := int(time.Until(c.NotAfter).Hours() / 24)
	return &certInfo{
		Subject: c.Subject.String(), Issuer: c.Issuer.String(),
		NotBefore: c.NotBefore.Format("2006-01-02"), NotAfter: c.NotAfter.Format("2006-01-02"),
		DaysLeft: left, Expired: time.Now().After(c.NotAfter), Names: names,
		SelfSign: c.Subject.String() == c.Issuer.String(),
	}
}

func schemaName(cn string) string { return seqPrefix.ReplaceAllString(cn, "") }

// serverGet reports the server's schemas, indexes, access rules, TLS and replication settings.
func (s *Server) serverGet(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	cc := sess.ConfigConn(configIdle)
	if cc == nil {
		fail(w, http.StatusConflict, "The server configuration is locked. Unlock it to see these settings.")
		return
	}
	ctx := s.overlayContext()
	st, err := loadOverlayState(cc, ctx.UsersBase)
	if err != nil {
		failLDAP(w, err)
		return
	}
	db, err := cc.GetAttrs(st.DBDN, []string{"*"})
	if err != nil {
		failLDAP(w, err)
		return
	}
	root, _ := cc.GetAttrs("cn=config", []string{"olcServerID", "olcTLSCertificateFile", "olcTLSCertificateKeyFile", "olcTLSCACertificateFile", "olcTLSCipherSuite", "olcTLSVerifyClient"})

	schemas := []string{}
	if es, err := cc.SearchAttrs("cn=schema,cn=config", "(objectClass=olcSchemaConfig)", []string{"cn"}, 0); err == nil {
		for _, e := range es {
			if n := schemaName(first(e.Values("cn"))); n != "" && n != "schema" {
				schemas = append(schemas, n)
			}
		}
	}
	sort.Strings(schemas)
	classes, _ := cc.ObjectClassNames()

	acl := []string{}
	for _, a := range db.Values("olcAccess") {
		acl = append(acl, strings.TrimSpace(seqPrefix.ReplaceAllString(a, "")))
	}
	tlsOut := map[string]any{"cert": "", "key": "", "ca": ""}
	var serverID []string
	if root != nil {
		tlsOut = map[string]any{
			"cert": first(root.Values("olcTLSCertificateFile")), "key": first(root.Values("olcTLSCertificateKeyFile")),
			"ca": first(root.Values("olcTLSCACertificateFile")), "ciphers": first(root.Values("olcTLSCipherSuite")),
		}
		serverID = root.Values("olcServerID")
	}
	cfg := s.c()
	if ci, err := servedCertificate(cfg.LDAP.URL, cfg.LDAP.StartTLS); err == nil {
		tlsOut["served"] = ci
	} else {
		tlsOut["servedError"] = err.Error()
	}

	repl := map[string]any{
		"serverID": nonNil(serverID), "syncrepl": nonNil(db.Values("olcSyncrepl")), "mirrorMode": strings.EqualFold(first(db.Values("olcMirrorMode")), "TRUE"),
		"provider": st.Overlays["syncprov"] != nil, "contextCSN": []string{},
	}
	if svc, err := s.svc(); err == nil {
		if e, err := svc.GetAttrs(st.Suffix, []string{"contextCSN"}); err == nil {
			repl["contextCSN"] = nonNil(e.Values("contextCSN"))
		}
		svc.Close()
	}

	writeJSON(w, map[string]any{
		"database": st.DBDN, "suffix": st.Suffix, "rootDN": first(db.Values("olcRootDN")),
		"schemas": schemas, "sshKeysSchema": classes["ldappublickey"],
		"indexes": parseIndexes(db.Values("olcDbIndex")), "acl": acl, "tls": tlsOut, "replication": repl,
		"serviceAccount": cfg.LDAP.BindDN,
	})
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ---- changes ----

type serverRequest struct {
	Kind      string   `json:"kind"` // schema.sshkeys, index.add, index.remove, acl.set, tls.set
	Attr      string   `json:"attr"`
	Types     []string `json:"types"`
	Rules     []string `json:"rules"`
	Cert      string   `json:"cert"`
	Key       string   `json:"key"`
	CA        string   `json:"ca"`
	Confirmed bool     `json:"confirmed"`
}

const sshSchemaLDIFAttr = "( 1.3.6.1.4.1.24552.500.1.1.1.13 NAME 'sshPublicKey' DESC 'MANDATORY: OpenSSH Public key' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )"
const sshSchemaLDIFClass = "( 1.3.6.1.4.1.24552.500.1.1.2.0 NAME 'ldapPublicKey' DESC 'MANDATORY: OpenSSH LPK objectclass' SUP top AUXILIARY MAY ( sshPublicKey $ uid ) )"

// buildServerPlan turns a request into the exact directory changes, plus a text to put at the end of the backup
// that undoes it.
func (s *Server) buildServerPlan(cc *ldapclient.Client, st *overlays.State, req serverRequest) (*overlays.Plan, string, error) {
	p := &overlays.Plan{Summary: []string{}, Warnings: []string{}}
	db, err := cc.GetAttrs(st.DBDN, []string{"*"})
	if err != nil {
		return nil, "", err
	}
	mod := func(dn string, mods ...overlays.Mod) {
		p.Ops = append(p.Ops, overlays.Op{Target: "config", Kind: "modify", DN: dn, Mods: mods})
	}
	switch req.Kind {
	case "schema.sshkeys":
		classes, _ := cc.ObjectClassNames()
		if classes["ldappublickey"] {
			return nil, "", validationError("The server already understands SSH public keys (the ldapPublicKey class is loaded).")
		}
		p.Ops = append(p.Ops, overlays.Op{Target: "config", Kind: "add", DN: "cn=openssh-lpk,cn=schema,cn=config", Attrs: map[string][]string{
			"objectClass": {"olcSchemaConfig"}, "cn": {"openssh-lpk"},
			"olcAttributeTypes": {sshSchemaLDIFAttr}, "olcObjectClasses": {sshSchemaLDIFClass},
		}})
		p.Summary = append(p.Summary, "Teach the server about SSH public keys by adding the standard OpenSSH “openssh-lpk” schema (the sshPublicKey attribute and the ldapPublicKey class).")
		p.Warnings = append(p.Warnings, "Schemas can be added but not removed while the server is running. This one only adds new names, so it won't affect existing data.")
		return p, "", nil

	case "index.add", "index.remove":
		attr := strings.TrimSpace(req.Attr)
		if !idxAttrRE.MatchString(attr) {
			return nil, "", validationError("“" + attr + "” isn't a valid attribute name.")
		}
		lines := parseIndexes(db.Values("olcDbIndex"))
		var hit *indexLine
		for i := range lines {
			for _, a := range lines[i].Attrs {
				if strings.EqualFold(a, attr) {
					hit = &lines[i]
				}
			}
		}
		if req.Kind == "index.add" {
			if hit != nil {
				return nil, "", validationError("“" + attr + "” is already indexed (" + hit.Raw + "). Remove that index first if you want to change it.")
			}
			types, err := cleanIndexTypes(req.Types)
			if err != nil {
				return nil, "", err
			}
			mod(st.DBDN, overlays.Mod{Op: "add", Attr: "olcDbIndex", Values: []string{attr + " " + strings.Join(types, ",")}})
			p.Summary = append(p.Summary, fmt.Sprintf("Index “%s” for %s searches, so looking people up by it is fast.", attr, strings.Join(types, ", ")))
			p.Warnings = append(p.Warnings, "Entries that already exist may not be added to the new index until the server re-indexes them (slapindex, with the server stopped) or they are next changed. Until then searches that rely on the index may miss them. Check a search after turning this on.")
			return p, "", nil
		}
		if hit == nil {
			return nil, "", validationError("“" + attr + "” isn't indexed.")
		}
		for _, a := range hit.Attrs {
			switch strings.ToLower(a) {
			case "objectclass", "entrycsn", "entryuuid":
				return nil, "", validationError("The index on “" + a + "” is needed by the server itself (and by replication) and can't be removed here.")
			}
		}
		mods := []overlays.Mod{{Op: "delete", Attr: "olcDbIndex", Values: findIndexValue(db.Values("olcDbIndex"), hit.Raw)}}
		var rest []string
		for _, a := range hit.Attrs {
			if !strings.EqualFold(a, attr) {
				rest = append(rest, a)
			}
		}
		if len(rest) > 0 {
			mods = append(mods, overlays.Mod{Op: "add", Attr: "olcDbIndex", Values: []string{strings.Join(rest, ",") + " " + strings.Join(hit.Types, ",")}})
		}
		mod(st.DBDN, mods...)
		p.Summary = append(p.Summary, "Stop indexing “"+attr+"”.")
		p.Warnings = append(p.Warnings, "Searches that use this attribute may become slow on a large directory. They keep working.")
		return p, "", nil

	case "acl.service-read":
		rules, changed, err := insertServiceRead(db.Values("olcAccess"), "ou=services,"+st.Suffix)
		if err != nil {
			return nil, "", err
		}
		if !changed {
			return nil, "", validationError("The access rules already let service accounts read the directory.")
		}
		req.Rules = rules
		fallthrough
	case "acl.set":
		rules, warn, err := s.checkACL(req.Rules, db.Values("olcRootDN"))
		if err != nil {
			return nil, "", err
		}
		numbered := make([]string, len(rules))
		for i, r := range rules {
			numbered[i] = fmt.Sprintf("{%d}%s", i, r)
		}
		mod(st.DBDN, overlays.Mod{Op: "replace", Attr: "olcAccess", Values: numbered})
		p.Summary = append(p.Summary, fmt.Sprintf("Replace the directory's access rules with these %d rules.", len(rules)))
		if req.Kind == "acl.service-read" {
			p.Summary = []string{"Let service accounts (every account under ou=services," + st.Suffix + ") read the directory, by adding one clause to the catch-all access rule. Password hashes stay hidden: an earlier rule already protects them. Nothing else about the rules changes."}
		}
		p.Warnings = append(p.Warnings, "A mistake here can stop people, applications or Dapper itself from reading or changing the directory, and fixing it may require editing the server's configuration on the server directly. A copy of the current rules is saved first and shown in the backup.")
		p.Warnings = append(p.Warnings, warn...)
		return p, "", nil

	case "tls.set":
		cert, key, ca := strings.TrimSpace(req.Cert), strings.TrimSpace(req.Key), strings.TrimSpace(req.CA)
		for _, v := range []string{cert, key, ca} {
			if v != "" && (!pathOKRE.MatchString(v) || strings.Contains(v, "..")) {
				return nil, "", validationError("“" + v + "” isn't a valid absolute file path (letters, numbers and . _ - / only).")
			}
		}
		if (cert == "") != (key == "") {
			return nil, "", validationError("Set the certificate and its private key together.")
		}
		cur, _ := cc.GetAttrs("cn=config", []string{"olcTLSCertificateFile", "olcTLSCertificateKeyFile", "olcTLSCACertificateFile"})
		undo := "# To undo the TLS change, apply:\n# dn: cn=config\n# changetype: modify\n"
		var mods []overlays.Mod
		for _, f := range []struct{ attr, val string }{{"olcTLSCertificateFile", cert}, {"olcTLSCertificateKeyFile", key}, {"olcTLSCACertificateFile", ca}} {
			old := ""
			if cur != nil {
				old = first(cur.Values(f.attr))
			}
			if f.val == old {
				continue
			}
			switch {
			case f.val == "":
				mods = append(mods, overlays.Mod{Op: "delete", Attr: f.attr})
			default:
				mods = append(mods, overlays.Mod{Op: "replace", Attr: f.attr, Values: []string{f.val}})
			}
			if old == "" {
				undo += "# delete: " + f.attr + "\n# -\n"
			} else {
				undo += "# replace: " + f.attr + "\n# " + f.attr + ": " + old + "\n# -\n"
			}
		}
		if len(mods) == 0 {
			return nil, "", validationError("Those are already the current settings.")
		}
		mod("cn=config", mods...)
		p.Summary = append(p.Summary, "Change which certificate files the directory server uses for encrypted connections.")
		p.Warnings = append(p.Warnings, "The files must already exist on the directory server and be readable by the server's own user. If a path is wrong, encrypted connections (ldaps:// and StartTLS) may stop working, including Dapper's if it uses them. Depending on your OpenLDAP version the server may need a restart before it starts using new files.")
		return p, undo, nil
	}
	return nil, "", validationError("Unknown change.")
}

func cleanIndexTypes(in []string) ([]string, error) {
	ok := map[string]bool{"pres": true, "eq": true, "approx": true, "sub": true}
	var out []string
	seen := map[string]bool{}
	for _, t := range in {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !seen[t] {
			if !ok[t] {
				return nil, validationError("“" + t + "” isn't an index type. Choose from pres, eq, approx and sub.")
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, validationError("Choose at least one kind of search to speed up.")
	}
	sort.Strings(out)
	return out, nil
}

// findIndexValue returns the stored value (with its {n} prefix) whose text matches raw.
func findIndexValue(vals []string, raw string) []string {
	for _, v := range vals {
		if strings.Join(strings.Fields(seqPrefix.ReplaceAllString(strings.TrimSpace(v), "")), " ") == raw {
			return []string{v}
		}
	}
	return []string{raw}
}

var aclRuleRE = regexp.MustCompile(`(?i)^to\s+\S.*\sby\s+\S`)

// checkACL validates access rules and returns them cleaned, with any extra warnings.
func (s *Server) checkACL(in []string, rootDN []string) ([]string, []string, error) {
	var rules, warn []string
	for _, r := range in {
		r = strings.TrimSpace(seqPrefix.ReplaceAllString(strings.TrimSpace(r), ""))
		if r == "" {
			continue
		}
		if strings.ContainsAny(r, "\r\n\x00") || len(r) > 2000 {
			return nil, nil, validationError("Each rule must be a single line.")
		}
		if !aclRuleRE.MatchString(r) {
			return nil, nil, validationError("“" + shorten(r) + "” doesn't look like an access rule. Rules look like: to attrs=userPassword by self write by anonymous auth by * none")
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		return nil, nil, validationError("Enter at least one rule. With none, the server falls back to allowing everyone to read everything.")
	}
	if len(rules) > 100 {
		return nil, nil, validationError("That's more than 100 rules.")
	}
	joined := strings.ToLower(strings.Join(rules, "\n"))
	if !strings.Contains(joined, "userpassword") {
		warn = append(warn, "None of these rules mention userPassword. Check that people can still sign in and that nobody can read passwords.")
	}
	if !strings.Contains(joined, "anonymous auth") && !strings.Contains(joined, "by * auth") {
		warn = append(warn, "No rule lets sign-ins reach the password check (“by anonymous auth” or “by * auth”). People may not be able to sign in.")
	}
	svc := strings.ToLower(s.c().LDAP.BindDN)
	isRoot := false
	for _, r := range rootDN {
		isRoot = isRoot || strings.EqualFold(r, s.c().LDAP.BindDN)
	}
	if !isRoot && svc != "" && !strings.Contains(joined, svc) {
		warn = append(warn, "Dapper's service account ("+s.c().LDAP.BindDN+") isn't named in any rule and isn't the server's root account, so these rules might lock Dapper out. Add a rule giving it the access it needs.")
	}
	return rules, warn, nil
}

func shorten(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

func (s *Server) serverPreview(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req serverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cc := sess.ConfigConn(configIdle)
	if cc == nil {
		fail(w, http.StatusConflict, "The server configuration is locked. Unlock it again to continue.")
		return
	}
	st, err := loadOverlayState(cc, s.overlayContext().UsersBase)
	if err != nil {
		failLDAP(w, err)
		return
	}
	p, _, err := s.buildServerPlan(cc, st, req)
	if err != nil {
		failLDAP(w, err)
		return
	}
	writeJSON(w, map[string]any{"summary": p.Summary, "warnings": p.Warnings, "ldif": p.LDIF("config")})
}

func (s *Server) serverApply(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var req serverRequest
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
	st, err := loadOverlayState(cc, s.overlayContext().UsersBase)
	if err != nil {
		failLDAP(w, err)
		return
	}
	p, undo, err := s.buildServerPlan(cc, st, req)
	if err != nil {
		failLDAP(w, err)
		return
	}
	var extra []string
	if undo != "" {
		extra = append(extra, undo)
	}
	backup, err := s.backupConfig(cc, st, extra...)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save a backup of the server's current configuration, so nothing was changed: "+err.Error())
		return
	}
	for i, op := range p.Ops {
		if err := applyOp(cc, op); err != nil {
			audit(sess, "server.failed", req.Kind)
			code, msg := friendlyOverlayError(err, req.Kind)
			writeJSONStatus(w, code, map[string]any{
				"error":  fmt.Sprintf("%s %d of %d steps were completed before this. A copy of the previous configuration is saved as %s.", msg, i, len(p.Ops), backup),
				"backup": backup,
			})
			return
		}
	}
	audit(sess, "server."+req.Kind, st.DBDN)
	writeJSON(w, map[string]any{"ok": true, "backup": backup})
}
