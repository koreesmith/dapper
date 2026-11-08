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
	Feature      string `json:"feature,omitempty"`  // "unix" or "ssh": only offered when that feature is on
	ReadOnly     bool   `json:"readOnly,omitempty"` // shown to admins but never edited here (e.g. uidNumber)
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
	// Unix login details (shown only when Unix accounts are turned on).
	{Attr: "uidNumber", Label: "User ID (UID)", Type: "text", Feature: "unix", ReadOnly: true, UserVisible: true},
	{Attr: "gidNumber", Label: "Primary group ID (GID)", Type: "text", Feature: "unix", ReadOnly: true, UserVisible: true},
	{Attr: "homeDirectory", Label: "Home directory", Type: "path", Feature: "unix", UserVisible: true},
	{Attr: "loginShell", Label: "Login shell", Type: "path", Feature: "unix", UserVisible: true},
	{Attr: "sshPublicKey", Label: "SSH public keys", Type: "sshkey", Multi: true, Feature: "ssh", UserVisible: true, UserEditable: true},
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
		if f.ReadOnly {
			f.UserEditable = false
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
	p.mu.RLock()
	next := make(map[string]override, len(p.over))
	for k, v := range p.over {
		next[k] = v // keep choices for fields not shown (e.g. a feature that's switched off)
	}
	p.mu.RUnlock()
	for _, f := range in {
		if _, ok := lookup(f.Attr); !ok {
			return fmt.Errorf("unknown field %q", f.Attr)
		}
		def, _ := lookup(f.Attr)
		next[f.Attr] = override{UserVisible: f.UserVisible || f.UserEditable, UserEditable: f.UserEditable && !def.ReadOnly}
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

var (
	phoneRE = regexp.MustCompile(`^[0-9+().\-\s]{3,32}$`)
	pathRE  = regexp.MustCompile(`^/[A-Za-z0-9._/@+-]*$`)
	sshRE   = regexp.MustCompile(`^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]+={0,2}( .{0,200})?$`)
)

// Validate checks values for a field and returns a friendly error.
func Validate(f Field, values []string) error {
	for _, v := range values {
		if len(v) > 256 && f.Type != "textarea" && f.Type != "sshkey" || len(v) > 8192 {
			return fmt.Errorf("%s is too long", f.Label)
		}
		switch f.Type {
		case "email":
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				return fmt.Errorf("%q is not a valid email address", v)
			}
		case "path":
			if !pathRE.MatchString(v) {
				return fmt.Errorf("%q isn't a valid absolute path (use letters, numbers and . _ - / only)", v)
			}
		case "sshkey":
			if !sshRE.MatchString(v) || strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("that doesn't look like an SSH public key (it should start with ssh-ed25519, ssh-rsa or ecdsa-sha2-... followed by the key)")
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
