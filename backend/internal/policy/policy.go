// Package policy defines which directory attributes the friendly UI exposes
// and which of them ordinary users may see and change. Admin overrides are
// persisted to a YAML file in the data directory.
package policy

import (
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Field describes one attribute shown in the UI.
type Field struct {
	Attr         string `json:"attr"`
	Label        string `json:"label"`
	Type         string `json:"type"` // text, email, tel, textarea
	Multi        bool   `json:"multi"`
	Required     bool   `json:"required,omitempty"` // needed when creating an account
	UserVisible  bool   `json:"userVisible"`        // user can see it on their own profile
	UserEditable bool   `json:"userEditable"`       // user can change it
}

var defaults = []Field{
	{Attr: "givenName", Label: "First name", Type: "text", Required: true, UserVisible: true},
	{Attr: "sn", Label: "Last name", Type: "text", Required: true, UserVisible: true},
	{Attr: "displayName", Label: "Display name", Type: "text", UserVisible: true},
	{Attr: "mail", Label: "Email", Type: "email", Multi: true, UserVisible: true, UserEditable: true},
	{Attr: "telephoneNumber", Label: "Phone", Type: "tel", Multi: true, UserVisible: true, UserEditable: true},
	{Attr: "mobile", Label: "Mobile phone", Type: "tel", Multi: true, UserVisible: true, UserEditable: true},
	{Attr: "title", Label: "Job title", Type: "text", UserVisible: true},
	{Attr: "departmentNumber", Label: "Department", Type: "text", UserVisible: true},
	{Attr: "street", Label: "Street address", Type: "text", UserVisible: true, UserEditable: true},
	{Attr: "l", Label: "City", Type: "text", UserVisible: true, UserEditable: true},
	{Attr: "st", Label: "State / province", Type: "text", UserVisible: true, UserEditable: true},
	{Attr: "postalCode", Label: "Postal code", Type: "text", UserVisible: true, UserEditable: true},
	{Attr: "description", Label: "Notes (admins only)", Type: "textarea"},
}

type override struct {
	UserVisible  bool `yaml:"userVisible"`
	UserEditable bool `yaml:"userEditable"`
}

// Policy is safe for concurrent use.
type Policy struct {
	mu   sync.RWMutex
	path string
	over map[string]override
}

// Load reads overrides from dir/policy.yaml if present.
func Load(dir string) (*Policy, error) {
	p := &Policy{path: filepath.Join(dir, "policy.yaml"), over: map[string]override{}}
	b, err := os.ReadFile(p.path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, &p.over); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p.path, err)
	}
	return p, nil
}

// Fields returns the catalog with overrides applied.
func (p *Policy) Fields() []Field {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Field, len(defaults))
	for i, f := range defaults {
		if o, ok := p.over[f.Attr]; ok {
			f.UserVisible, f.UserEditable = o.UserVisible, o.UserEditable
		}
		if f.UserEditable {
			f.UserVisible = true // you cannot edit what you cannot see
		}
		out[i] = f
	}
	return out
}

// Field looks up one attribute (case-insensitive).
func (p *Policy) Field(attr string) (Field, bool) {
	for _, f := range p.Fields() {
		if strings.EqualFold(f.Attr, attr) {
			return f, true
		}
	}
	return Field{}, false
}

// Update replaces the user-visibility settings and persists them.
func (p *Policy) Update(in []Field) error {
	next := map[string]override{}
	for _, f := range in {
		if _, ok := lookup(f.Attr); !ok {
			return fmt.Errorf("unknown field %q", f.Attr)
		}
		next[f.Attr] = override{UserVisible: f.UserVisible || f.UserEditable, UserEditable: f.UserEditable}
	}
	b, err := yaml.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o750); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return err
	}
	p.mu.Lock()
	p.over = next
	p.mu.Unlock()
	return nil
}

func lookup(attr string) (Field, bool) {
	for _, f := range defaults {
		if strings.EqualFold(f.Attr, attr) {
			return f, true
		}
	}
	return Field{}, false
}

var phoneRE = regexp.MustCompile(`^[0-9+().\-\s]{3,32}$`)

// Validate checks values for a field and returns a friendly error.
func Validate(f Field, values []string) error {
	for _, v := range values {
		if len(v) > 256 && f.Type != "textarea" || len(v) > 2048 {
			return fmt.Errorf("%s is too long", f.Label)
		}
		switch f.Type {
		case "email":
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				return fmt.Errorf("%q is not a valid email address", v)
			}
		case "tel":
			if !phoneRE.MatchString(v) {
				return fmt.Errorf("%q is not a valid phone number", v)
			}
		}
		if strings.ContainsAny(v, "\x00") {
			return fmt.Errorf("%s contains invalid characters", f.Label)
		}
	}
	return nil
}

// Clean trims and drops empty values.
func Clean(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
