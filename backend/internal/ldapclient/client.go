// Package ldapclient wraps go-ldap with the operations Dapper needs.
package ldapclient

import (
	"crypto/tls"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/go-ldap/ldap/v3"
)

// Server describes how to reach a directory (no credentials).
type Server struct {
	URL        string // ldap://host:389 or ldaps://host:636
	StartTLS   bool
	SkipVerify bool
}

// Entry is a directory entry with multi-valued attributes.
type Entry struct {
	DN    string              `json:"dn"`
	Attrs map[string][]string `json:"attrs"`
}

// Change is a single attribute modification.
type Change struct {
	Op     string   `json:"op"` // add, replace, delete
	Attr   string   `json:"attr"`
	Values []string `json:"values"`
}

type Client struct {
	mu   sync.Mutex
	conn *ldap.Conn
}

// Dial connects (with optional StartTLS) but does not bind.
func Dial(srv Server) (*Client, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: srv.SkipVerify} //nolint:gosec // operator opt-in
	conn, err := ldap.DialURL(srv.URL, ldap.DialWithTLSConfig(tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if srv.StartTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	return &Client{conn: conn}, nil
}

// Bind authenticates the connection. An empty password is rejected so that
// an "unauthenticated bind" can never be mistaken for a successful login.
func (c *Client) Bind(dn, password string) error {
	if dn == "" || password == "" {
		return ErrInvalidCredentials
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.Bind(dn, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return ErrInvalidCredentials
		}
		return err
	}
	return nil
}

// ErrInvalidCredentials is returned for a failed bind.
var ErrInvalidCredentials = errors.New("invalid credentials")

// FindUserDN returns the DN of the single user under base matching any of
// attrs equal to login. Returns ErrInvalidCredentials if none or ambiguous.
func (c *Client) FindUserDN(base string, attrs []string, login string) (string, error) {
	var parts string
	for _, a := range attrs {
		parts += fmt.Sprintf("(%s=%s)", a, ldap.EscapeFilter(login))
	}
	filter := "(&(objectClass=person)(|" + parts + "))"
	es, err := c.search(base, ldap.ScopeWholeSubtree, filter, []string{"dn"}, 2)
	if err != nil {
		return "", err
	}
	if len(es) != 1 {
		return "", ErrInvalidCredentials
	}
	return es[0].DN, nil
}

// IsMember reports whether memberDN is listed in the group groupDN.
func (c *Client) IsMember(groupDN, memberDN string) (bool, error) {
	e := ldap.EscapeFilter(memberDN)
	filter := fmt.Sprintf("(|(member=%s)(uniqueMember=%s))", e, e)
	es, err := c.search(groupDN, ldap.ScopeBaseObject, filter, []string{"dn"}, 1)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return false, nil
		}
		return false, err
	}
	return len(es) == 1, nil
}

func (c *Client) Close() { c.conn.Close() }

func toEntries(res *ldap.SearchResult) []Entry {
	out := make([]Entry, 0, len(res.Entries))
	for _, e := range res.Entries {
		a := map[string][]string{}
		for _, at := range e.Attributes {
			if strings.EqualFold(at.Name, "userPassword") {
				continue // never send password data to the browser
			}
			a[at.Name] = at.Values
		}
		out = append(out, Entry{DN: e.DN, Attrs: a})
	}
	return out
}

// pageSize is how many entries to ask for per page. Paging also gets past the
// server-wide size limit (500 by default on OpenLDAP) that applies to ordinary,
// non-root accounts and would otherwise silently cut results short.
var pageSize uint32 = 250

// searchRaw runs a search, following result pages until limit entries are
// collected (0 means all). sizeLimited is true if the server cut the results
// short with its size limit, which paging avoids when the server supports it.
func (c *Client) searchRaw(base string, scope int, filter string, attrs []string, limit int) (out []Entry, sizeLimited bool, err error) {
	if scope == ldap.ScopeBaseObject || limit == 1 {
		req := ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, limit, 0, false, filter, attrs, nil)
		res, err := c.conn.Search(req)
		if err != nil {
			if res == nil || !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
				return nil, false, err
			}
			sizeLimited = true
		}
		return toEntries(res), sizeLimited, nil
	}
	paging := ldap.NewControlPaging(pageSize)
	for {
		req := ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, 0, 0, false, filter, attrs, []ldap.Control{paging})
		res, err := c.conn.Search(req)
		if err != nil {
			if res == nil || !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
				return nil, false, err
			}
			sizeLimited = true // the server doesn't page and cut us off
		}
		out = append(out, toEntries(res)...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], sizeLimited, nil
		}
		pc, ok := ldap.FindControl(res.Controls, ldap.ControlTypePaging).(*ldap.ControlPaging)
		if sizeLimited || !ok || len(pc.Cookie) == 0 {
			return out, sizeLimited, nil
		}
		paging.SetCookie(pc.Cookie)
	}
}

func (c *Client) search(base string, scope int, filter string, attrs []string, limit int) ([]Entry, error) {
	es, _, err := c.searchRaw(base, scope, filter, attrs, limit)
	return es, err
}

// ErrIncomplete means the directory cut a search short, so the answer can't be trusted.
var ErrIncomplete = errors.New("the directory's search size limit cut the results short")

// SearchComplete is like SearchAttrs but fails rather than return a truncated
// answer. Use it when a missing entry could cause harm (for example when
// choosing an unused uid).
func (c *Client) SearchComplete(base, filter string, attrs []string) ([]Entry, error) {
	es, limited, err := c.searchRaw(base, ldap.ScopeWholeSubtree, filter, attrs, 0)
	if err != nil {
		return nil, err
	}
	if limited {
		return nil, ErrIncomplete
	}
	return es, nil
}

var ocNameRE = regexp.MustCompile(`NAME\s+(?:'([^']+)'|\(\s*((?:'[^']+'\s*)+)\))`)

// ObjectClassNames returns the (lower-cased) object class names the server's schema defines.
func (c *Client) ObjectClassNames() (map[string]bool, error) {
	es, err := c.search("", ldap.ScopeBaseObject, "(objectClass=*)", []string{"subschemaSubentry"}, 1)
	if err != nil || len(es) == 0 {
		return nil, err
	}
	sub := first(es[0].Values("subschemaSubentry"), "cn=Subschema")
	se, err := c.GetAttrs(sub, []string{"objectClasses"})
	if err != nil {
		return nil, err
	}
	return ParseObjectClassNames(se.Values("objectClasses")), nil
}

// ParseObjectClassNames extracts the (lower-cased) names from schema definitions
// such as "( 1.2.3 NAME 'posixAccount' SUP top ... )" or "NAME ( 'a' 'b' )".
func ParseObjectClassNames(defs []string) map[string]bool {
	names := map[string]bool{}
	quoted := regexp.MustCompile(`'([^']+)'`)
	for _, def := range defs {
		m := ocNameRE.FindStringSubmatch(def)
		if m == nil {
			continue
		}
		if m[1] != "" {
			names[strings.ToLower(m[1])] = true
		}
		for _, n := range quoted.FindAllStringSubmatch(m[2], -1) {
			names[strings.ToLower(n[1])] = true
		}
	}
	return names
}

func first(v []string, def string) string {
	if len(v) > 0 {
		return v[0]
	}
	return def
}

// Children lists immediate children of dn.
func (c *Client) Children(dn string) ([]Entry, error) {
	return c.search(dn, ldap.ScopeSingleLevel, "(objectClass=*)", []string{"objectClass", "hasSubordinates"}, 0)
}

// Get returns one entry with all user attributes.
func (c *Client) Get(dn string) (*Entry, error) {
	es, err := c.search(dn, ldap.ScopeBaseObject, "(objectClass=*)", []string{"*"}, 1)
	if err != nil {
		return nil, err
	}
	if len(es) == 0 {
		return nil, fmt.Errorf("not found: %s", dn)
	}
	return &es[0], nil
}

// Search runs a subtree search under base.
func (c *Client) Search(base, filter string, limit int) ([]Entry, error) {
	if filter == "" {
		filter = "(objectClass=*)"
	}
	return c.search(base, ldap.ScopeWholeSubtree, filter, []string{"*"}, limit)
}

func (c *Client) Add(e Entry) error {
	req := ldap.NewAddRequest(e.DN, nil)
	for k, v := range e.Attrs {
		req.Attribute(k, v)
	}
	return c.conn.Add(req)
}

func (c *Client) Modify(dn string, changes []Change) error {
	req := ldap.NewModifyRequest(dn, nil)
	for _, ch := range changes {
		switch ch.Op {
		case "add":
			req.Add(ch.Attr, ch.Values)
		case "replace":
			req.Replace(ch.Attr, ch.Values)
		case "delete":
			req.Delete(ch.Attr, ch.Values)
		default:
			return fmt.Errorf("unknown op %q", ch.Op)
		}
	}
	return c.conn.Modify(req)
}

func (c *Client) Delete(dn string) error { return c.conn.Del(ldap.NewDelRequest(dn, nil)) }

// ListPeople returns up to limit person entries under base with the given
// attributes. truncated is true when more exist than were returned.
func (c *Client) ListPeople(base string, attrs []string, limit int) (entries []Entry, truncated bool, err error) {
	es, err := c.search(base, ldap.ScopeWholeSubtree, "(objectClass=person)", attrs, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(es) > limit {
		return es[:limit], true, nil
	}
	return es, false, nil
}

// GetAttrs returns one entry with the requested attributes (use "*" and
// operational names such as pwdAccountLockedTime explicitly).
func (c *Client) GetAttrs(dn string, attrs []string) (*Entry, error) {
	es, err := c.search(dn, ldap.ScopeBaseObject, "(objectClass=*)", attrs, 1)
	if err != nil {
		return nil, err
	}
	if len(es) == 0 {
		return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("not found"))
	}
	return &es[0], nil
}

// SearchAttrs runs a subtree search with an explicit filter and attribute list.
func (c *Client) SearchAttrs(base, filter string, attrs []string, limit int) ([]Entry, error) {
	return c.search(base, ldap.ScopeWholeSubtree, filter, attrs, limit)
}

// SetPassword sets a user's password with the Password Modify extended
// operation, so the server applies its own hashing and password policy.
func (c *Client) SetPassword(dn, newPassword string) error {
	_, err := c.conn.PasswordModify(&ldap.PasswordModifyRequest{UserIdentity: dn, NewPassword: newPassword})
	return err
}

// Move renames dn to newRDN under newParent (empty keeps the current parent).
func (c *Client) Move(dn, newRDN, newParent string) error {
	return c.conn.ModifyDN(ldap.NewModifyDNRequest(dn, newRDN, true, newParent))
}

// Group is a group entry with its member DNs.
type Group struct {
	DN      string   `json:"dn"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// Groups lists groups under base. memberAttr is e.g. "member".
func (c *Client) Groups(base, objectClass, memberAttr string) ([]Group, error) {
	es, err := c.search(base, ldap.ScopeWholeSubtree,
		fmt.Sprintf("(objectClass=%s)", ldap.EscapeFilter(objectClass)), []string{"cn", memberAttr}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(es))
	for _, e := range es {
		g := Group{DN: e.DN}
		if v := e.Attrs["cn"]; len(v) > 0 {
			g.Name = v[0]
		}
		g.Members = e.Attrs[memberAttr]
		out = append(out, g)
	}
	return out, nil
}

// GroupsOfMember returns the groups under base that list memberDN.
func (c *Client) GroupsOfMember(base, objectClass, memberAttr, memberDN string) ([]Group, error) {
	es, err := c.search(base, ldap.ScopeWholeSubtree,
		fmt.Sprintf("(&(objectClass=%s)(%s=%s))", ldap.EscapeFilter(objectClass), memberAttr, ldap.EscapeFilter(memberDN)),
		[]string{"cn"}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(es))
	for _, e := range es {
		g := Group{DN: e.DN}
		if v := e.Attrs["cn"]; len(v) > 0 {
			g.Name = v[0]
		}
		out = append(out, g)
	}
	return out, nil
}

// IsCode reports whether err carries the given LDAP result code.
func IsCode(err error, code uint16) bool { return ldap.IsErrorWithCode(err, code) }

// Result codes used by callers.
const (
	CodeNoSuchObject          = ldap.LDAPResultNoSuchObject
	CodeAlreadyExists         = ldap.LDAPResultEntryAlreadyExists
	CodeInsufficientAccess    = ldap.LDAPResultInsufficientAccessRights
	CodeConstraintViolation   = ldap.LDAPResultConstraintViolation
	CodeInvalidSyntax         = ldap.LDAPResultInvalidAttributeSyntax
	CodeObjectClassViolation  = ldap.LDAPResultObjectClassViolation
	CodeNotAllowedOnNonLeaf   = ldap.LDAPResultNotAllowedOnNonLeaf
	CodeUndefinedAttribute    = ldap.LDAPResultUndefinedAttributeType
	CodeNoSuchAttribute       = ldap.LDAPResultNoSuchAttribute
	CodeAttributeOrValueExist = ldap.LDAPResultAttributeOrValueExists
	CodeUnwillingToPerform    = ldap.LDAPResultUnwillingToPerform
)

// Values returns the values of attr (case-insensitive), or nil.
func (e Entry) Values(attr string) []string {
	for k, v := range e.Attrs {
		if strings.EqualFold(k, attr) {
			return v
		}
	}
	return nil
}

// Exists reports whether dn exists.
func (c *Client) Exists(dn string) (bool, error) {
	_, err := c.GetAttrs(dn, []string{"1.1"})
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NamingContexts reads the server's root DSE for the directory suffixes it serves.
func (c *Client) NamingContexts() ([]string, error) {
	es, err := c.search("", ldap.ScopeBaseObject, "(objectClass=*)", []string{"namingContexts"}, 1)
	if err != nil || len(es) == 0 {
		return nil, err
	}
	return es[0].Values("namingContexts"), nil
}
