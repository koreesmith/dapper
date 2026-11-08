// Package ldapclient wraps go-ldap with the operations Dapper needs.
package ldapclient

import (
	"crypto/tls"
	"fmt"

	"github.com/go-ldap/ldap/v3"
)

// Config describes how to reach and authenticate to a directory.
type Config struct {
	URL        string `json:"url"` // ldap://host:389 or ldaps://host:636
	BindDN     string `json:"bindDN"`
	Password   string `json:"password"`
	BaseDN     string `json:"baseDN"`
	StartTLS   bool   `json:"startTLS"`
	SkipVerify bool   `json:"skipVerify"`
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
	conn *ldap.Conn
	cfg  Config
}

// Connect dials and binds.
func Connect(cfg Config) (*Client, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.SkipVerify} //nolint:gosec // user opt-in
	conn, err := ldap.DialURL(cfg.URL, ldap.DialWithTLSConfig(tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if cfg.StartTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.Password); err != nil {
			conn.Close()
			return nil, fmt.Errorf("bind: %w", err)
		}
	} else if err := conn.UnauthenticatedBind(""); err != nil {
		conn.Close()
		return nil, fmt.Errorf("anonymous bind: %w", err)
	}
	return &Client{conn: conn, cfg: cfg}, nil
}

func (c *Client) Close()         { c.conn.Close() }
func (c *Client) BaseDN() string { return c.cfg.BaseDN }

func toEntries(res *ldap.SearchResult) []Entry {
	out := make([]Entry, 0, len(res.Entries))
	for _, e := range res.Entries {
		a := map[string][]string{}
		for _, at := range e.Attributes {
			a[at.Name] = at.Values
		}
		out = append(out, Entry{DN: e.DN, Attrs: a})
	}
	return out
}

func (c *Client) search(base string, scope int, filter string, attrs []string, limit int) ([]Entry, error) {
	req := ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, limit, 0, false, filter, attrs, nil)
	res, err := c.conn.Search(req)
	if err != nil && res == nil {
		return nil, err
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
