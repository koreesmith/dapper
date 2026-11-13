// Package users turns raw directory entries into friendly user records and
// implements plain-language search, sorting and paging over them.
package users

import (
	"sort"
	"strings"
	"unicode"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

// Attrs are the directory attributes needed to build a User.
var Attrs = []string{
	"uid", "cn", "displayName", "givenName", "sn", "mail",
	"telephoneNumber", "mobile", "title", "departmentNumber", "pwdAccountLockedTime",
}

type User struct {
	DN         string   `json:"dn"`
	Username   string   `json:"username"`
	Name       string   `json:"name"`
	Email      string   `json:"email"`
	Phones     []string `json:"phones"`
	GivenName  string   `json:"givenName,omitempty"`
	Surname    string   `json:"surname,omitempty"`
	Title      string   `json:"title,omitempty"`
	Department string   `json:"department,omitempty"`
	Suspended  bool     `json:"suspended"`
	HasPhoto   bool     `json:"hasPhoto,omitempty"`

	haystack []string // lowercased searchable text
	digits   []string // digits-only phone numbers
}

func first(e ldapclient.Entry, attrs ...string) string {
	for _, a := range attrs {
		for k, v := range e.Attrs {
			if strings.EqualFold(k, a) && len(v) > 0 && v[0] != "" {
				return v[0]
			}
		}
	}
	return ""
}

func all(e ldapclient.Entry, attr string) []string {
	for k, v := range e.Attrs {
		if strings.EqualFold(k, attr) {
			return v
		}
	}
	return nil
}

func onlyDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// FromEntry builds a User from a directory entry.
func FromEntry(e ldapclient.Entry) User {
	u := User{
		DN:         e.DN,
		Username:   first(e, "uid"),
		Name:       first(e, "displayName", "cn"),
		Email:      first(e, "mail"),
		GivenName:  first(e, "givenName"),
		Surname:    first(e, "sn"),
		Title:      first(e, "title"),
		Department: first(e, "departmentNumber"),
		Suspended:  first(e, "pwdAccountLockedTime") != "",
		Phones:     append(all(e, "telephoneNumber"), all(e, "mobile")...),
	}
	if u.Username == "" {
		u.Username = rdnValue(e.DN)
	}
	if u.Name == "" {
		u.Name = u.Username
	}
	text := []string{u.Username, u.Name, u.Email, u.Title, u.Department,
		first(e, "givenName"), first(e, "sn"), first(e, "cn")}
	text = append(text, u.Phones...)
	for _, t := range text {
		if t != "" {
			u.haystack = append(u.haystack, strings.ToLower(t))
		}
	}
	for _, p := range u.Phones {
		if d := onlyDigits(p); d != "" {
			u.digits = append(u.digits, d)
		}
	}
	return u
}

// Matches reports whether every whitespace-separated term in q matches the
// user: a case-insensitive substring of any field, or (for terms with 3+
// digits) a digit sequence found in a phone number, ignoring spacing and
// punctuation, so "555-0100" finds "+1 555 0100".
func (u User) Matches(q string) bool {
	for _, term := range strings.Fields(strings.ToLower(q)) {
		if !u.matchTerm(term) {
			return false
		}
	}
	return true
}

func (u User) matchTerm(term string) bool {
	for _, h := range u.haystack {
		if strings.Contains(h, term) {
			return true
		}
	}
	// A term that looks like a phone number (digits and punctuation only) also matches phone numbers however
	// they are written. A name like "bk555" has letters, so its digits mustn't match every 555 number.
	if d := onlyDigits(term); len(d) >= 3 && !strings.ContainsFunc(term, unicode.IsLetter) {
		for _, p := range u.digits {
			if strings.Contains(p, d) {
				return true
			}
		}
	}
	return false
}

// Query describes a list request.
type Query struct {
	Search   string
	Status   string // "", "active" or "suspended"
	Sort     string // name (default), username, email
	Desc     bool
	Page     int // 1-based
	PageSize int
}

type Page struct {
	Users    []User `json:"users"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

// Filtered returns everyone matching the query, sorted, without paging.
func Filtered(all []User, q Query) []User {
	matched := make([]User, 0, len(all))
	for _, u := range all {
		if q.Status == "active" && u.Suspended || q.Status == "suspended" && !u.Suspended {
			continue
		}
		if q.Search == "" || u.Matches(q.Search) {
			matched = append(matched, u)
		}
	}
	key := func(u User) string {
		switch q.Sort {
		case "username":
			return strings.ToLower(u.Username)
		case "email":
			return strings.ToLower(u.Email)
		default:
			return strings.ToLower(u.Name)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := key(matched[i]), key(matched[j])
		if a == b {
			a, b = strings.ToLower(matched[i].Username), strings.ToLower(matched[j].Username)
		}
		if q.Desc {
			return a > b
		}
		return a < b
	})
	return matched
}

// List filters, sorts and pages all.
func List(all []User, q Query) Page {
	if q.PageSize < 1 {
		q.PageSize = 25
	}
	if q.PageSize > 200 {
		q.PageSize = 200
	}
	if q.Page < 1 {
		q.Page = 1
	}
	matched := Filtered(all, q)
	total := len(matched)
	start := (q.Page - 1) * q.PageSize
	if start > total {
		start = total
	}
	end := start + q.PageSize
	if end > total {
		end = total
	}
	return Page{Users: matched[start:end], Total: total, Page: q.Page, PageSize: q.PageSize}
}

// rdnValue returns the value in the entry's own name, e.g. "koree" for cn=koree,ou=People,...
func rdnValue(dn string) string {
	for i := 0; i < len(dn); i++ {
		if dn[i] == '\\' {
			i++
		} else if dn[i] == ',' {
			dn = dn[:i]
			break
		}
	}
	if i := strings.IndexByte(dn, '='); i >= 0 {
		return strings.ReplaceAll(dn[i+1:], "\\", "")
	}
	return ""
}
