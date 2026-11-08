// Package ldapclient wraps go-ldap with the operations Dapper needs.
package ldapclient

import (
	"crypto/tls"
	"errors"
	"fmt"
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

func (c *Client) search(base string, scope int, filter string, attrs []string, limit int) ([]Entry, error) {
	req := ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, limit, 0, false, filter, attrs, nil)
	res, err := c.conn.Search(req)
	if err != nil {
		// A size-limit hit still returns the entries found so far; anything
		// else (access denied, no such object, ...) is a real failure.
		if res == nil || !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			return nil, err
		}
	}
	return toEntries(res), nil
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
