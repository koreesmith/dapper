// Package config holds Dapper's settings.
//
// Almost everything is configured in the web UI (first-run setup wizard, then the
// Settings page) and stored in <dataDir>/settings.yaml. A YAML file and environment
// variables remain available for automated deployments; anything set by an
// environment variable is "locked" and shown read-only in the UI.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings are the values administrators manage in the UI.
type Settings struct {
	LDAP     LDAP     `yaml:"ldap"`
	Users    Users    `yaml:"users"`
	Groups   Groups   `yaml:"groups"`
	Admin    Admin    `yaml:"admin"`
	Password Password `yaml:"password"`
	Session  Session  `yaml:"session"`
	Unix     Unix     `yaml:"unix"`
}

// Config is Settings plus process-level options that need a restart to change.
type Config struct {
	Listen  string `yaml:"listen"`
	Static  string `yaml:"static"`
	DataDir string `yaml:"dataDir"`

	Settings `yaml:",inline"`
}

type LDAP struct {
	URL          string `yaml:"url"`
	StartTLS     bool   `yaml:"startTLS"`
	SkipVerify   bool   `yaml:"skipVerify"`
	CAFile       string `yaml:"caFile"`       // certificate (PEM) Dapper trusts for the directory, e.g. a Dapper-run server's own
	BindDN       string `yaml:"bindDN"`       // the service account
	BindPassword string `yaml:"bindPassword"` // never sent to the browser
}

type Users struct {
	BaseDN          string   `yaml:"baseDN"`
	LoginAttributes []string `yaml:"loginAttributes"` // what a person may type to sign in
	ObjectClasses   []string `yaml:"objectClasses"`   // applied to new accounts
	RDNAttribute    string   `yaml:"rdnAttribute"`    // naming attribute for new accounts
	// SuspendedBaseDN, if set, makes "suspend" move the account into this container
	// (works on any server). If empty, suspend locks the account with the ppolicy
	// overlay's pwdAccountLockedTime attribute.
	SuspendedBaseDN string `yaml:"suspendedBaseDN"`
}

type Groups struct {
	BaseDN          string `yaml:"baseDN"`          // empty disables group management
	ObjectClass     string `yaml:"objectClass"`     // default groupOfNames
	MemberAttribute string `yaml:"memberAttribute"` // default member
}

type Admin struct {
	GroupDN string `yaml:"groupDN"` // members of this group get the admin screens
}

type Password struct {
	MinLength int `yaml:"minLength"`
	// PolicyDN is the directory's password policy entry (ppolicy). Dapper reads it to tell people the
	// rules and when their password expires. It is filled in when password rules are turned on from the
	// Server page; leave empty if the directory has no password policy.
	PolicyDN string `yaml:"policyDN"`
}

// Unix controls Linux/Unix login support: new accounts get posixAccount attributes
// and a private group, so they work for logins on Linux servers.
type Unix struct {
	Enabled      bool   `yaml:"enabled"`
	UIDMin       int    `yaml:"uidMin"`       // new numeric ids are allocated inside [UIDMin, UIDMax]
	UIDMax       int    `yaml:"uidMax"`       //
	HomePattern  string `yaml:"homePattern"`  // %u is replaced by the username
	DefaultShell string `yaml:"defaultShell"` //
	Shadow       bool   `yaml:"shadow"`       // also add the shadowAccount object class
	SSHKeys      bool   `yaml:"sshKeys"`      // manage SSH public keys (ldapPublicKey)
}

type Session struct {
	IdleTimeout  time.Duration `yaml:"idleTimeout"`
	MaxAge       time.Duration `yaml:"maxAge"`
	SecureCookie *bool         `yaml:"secureCookie"` // default: auto (true over HTTPS)
}

// Required lists the settings that must be filled in before Dapper can run.
func (c *Config) Required() map[string]string {
	return map[string]string{
		"ldap.url": c.LDAP.URL, "ldap.bindDN": c.LDAP.BindDN, "ldap.bindPassword": c.LDAP.BindPassword,
		"users.baseDN": c.Users.BaseDN, "admin.groupDN": c.Admin.GroupDN,
	}
}

// Missing returns the names of required settings that are empty.
func (c *Config) Missing() []string {
	var m []string
	for _, k := range []string{"ldap.url", "ldap.bindDN", "ldap.bindPassword", "users.baseDN", "admin.groupDN"} {
		if c.Required()[k] == "" {
			m = append(m, k)
		}
	}
	return m
}

// WithDefaults fills in unset optional values.
func (c *Config) WithDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if len(c.Users.ObjectClasses) == 0 {
		c.Users.ObjectClasses = []string{"inetOrgPerson"}
	}
	if c.Users.RDNAttribute == "" {
		c.Users.RDNAttribute = "uid"
	}
	if len(c.Users.LoginAttributes) == 0 {
		c.Users.LoginAttributes = []string{"uid", "mail"}
	}
	if c.Groups.ObjectClass == "" {
		c.Groups.ObjectClass = "groupOfNames"
	}
	if c.Groups.MemberAttribute == "" {
		c.Groups.MemberAttribute = "member"
	}
	if c.Password.MinLength == 0 {
		c.Password.MinLength = 8
	}
	if c.Unix.UIDMin == 0 {
		c.Unix.UIDMin = 10000
	}
	if c.Unix.UIDMax == 0 {
		c.Unix.UIDMax = 59999
	}
	if c.Unix.HomePattern == "" {
		c.Unix.HomePattern = "/home/%u"
	}
	if c.Unix.DefaultShell == "" {
		c.Unix.DefaultShell = "/bin/bash"
	}
	if c.Session.IdleTimeout == 0 {
		c.Session.IdleTimeout = 30 * time.Minute
	}
	if c.Session.MaxAge == 0 {
		c.Session.MaxAge = 8 * time.Hour
	}
}

// Validate checks values that have limits.
func (c *Config) Validate() error {
	if c.Password.MinLength < 1 || c.Password.MinLength > 128 {
		return errors.New("the minimum password length must be between 1 and 128")
	}
	if c.Session.IdleTimeout < time.Minute || c.Session.MaxAge < c.Session.IdleTimeout {
		return errors.New("the idle timeout must be at least a minute and no longer than the maximum session length")
	}
	if c.Unix.Enabled {
		u := c.Unix
		switch {
		case c.Groups.BaseDN == "":
			return errors.New("Unix accounts need a groups container, because each person gets a private group there")
		case u.UIDMin < 1 || u.UIDMax <= u.UIDMin || u.UIDMax > 2147483647:
			return errors.New("the Unix ID range must be positive, with the end after the start")
		case !strings.HasPrefix(u.HomePattern, "/") || strings.ContainsAny(u.HomePattern, " \t\n"):
			return errors.New("the home directory pattern must be an absolute path without spaces, such as /home/%u")
		case !strings.HasPrefix(u.DefaultShell, "/") || strings.ContainsAny(u.DefaultShell, " \t\n"):
			return errors.New("the default login shell must be an absolute path such as /bin/bash")
		}
	}
	for _, u := range []string{c.LDAP.URL} {
		if u != "" && !strings.HasPrefix(u, "ldap://") && !strings.HasPrefix(u, "ldaps://") {
			return errors.New("the server address must start with ldap:// or ldaps://")
		}
	}
	return nil
}

// Store holds the live configuration. Get returns an immutable snapshot.
type Store struct {
	mu     sync.RWMutex
	cfg    *Config
	locked map[string]bool
	path   string // settings.yaml; empty for in-memory stores (tests)
}

// Static wraps an already-built config (used by tests).
func Static(c *Config) *Store { c.WithDefaults(); return &Store{cfg: c, locked: map[string]bool{}} }

// Open loads optional YAML, then UI-saved settings, then environment overrides.
// A missing or incomplete configuration is not an error: Dapper starts in setup mode.
func Open(yamlPath string) (*Store, error) {
	c := &Config{}
	if yamlPath != "" {
		b, err := os.ReadFile(yamlPath)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	// The data directory can come from the file or the environment.
	if v, ok := os.LookupEnv("DAPPER_DATA_DIR"); ok {
		c.DataDir = v
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	s := &Store{locked: map[string]bool{}, path: filepath.Join(c.DataDir, "settings.yaml")}
	if b, err := os.ReadFile(s.path); err == nil {
		if err := yaml.Unmarshal(b, &c.Settings); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.applyEnv(c)
	c.WithDefaults()
	s.cfg = c
	return s, nil
}

// applyEnv overlays environment variables and records which settings they lock.
func (s *Store) applyEnv(c *Config) {
	str := func(dst *string, env, key string) {
		if v, ok := os.LookupEnv(env); ok {
			*dst = v
			if key != "" {
				s.locked[key] = true
			}
		}
	}
	boolean := func(dst *bool, env, key string) {
		if v, ok := os.LookupEnv(env); ok {
			*dst, _ = strconv.ParseBool(v)
			s.locked[key] = true
		}
	}
	str(&c.Listen, "DAPPER_LISTEN", "")
	str(&c.Static, "DAPPER_STATIC", "")
	str(&c.LDAP.URL, "DAPPER_LDAP_URL", "ldap.url")
	boolean(&c.LDAP.StartTLS, "DAPPER_LDAP_STARTTLS", "ldap.startTLS")
	boolean(&c.LDAP.SkipVerify, "DAPPER_LDAP_SKIP_VERIFY", "ldap.skipVerify")
	str(&c.LDAP.BindDN, "DAPPER_LDAP_BIND_DN", "ldap.bindDN")
	str(&c.LDAP.BindPassword, "DAPPER_LDAP_BIND_PASSWORD", "ldap.bindPassword")
	str(&c.Users.BaseDN, "DAPPER_USERS_BASE_DN", "users.baseDN")
	str(&c.Users.SuspendedBaseDN, "DAPPER_USERS_SUSPENDED_BASE_DN", "users.suspendedBaseDN")
	if v, ok := os.LookupEnv("DAPPER_USERS_LOGIN_ATTRIBUTES"); ok {
		c.Users.LoginAttributes = strings.Split(v, ",")
		s.locked["users.loginAttributes"] = true
	}
	str(&c.Groups.BaseDN, "DAPPER_GROUPS_BASE_DN", "groups.baseDN")
	str(&c.Admin.GroupDN, "DAPPER_ADMIN_GROUP_DN", "admin.groupDN")
}

// Get returns the current configuration snapshot. Callers must not modify it.
func (s *Store) Get() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Configured reports whether all required settings are present.
func (s *Store) Configured() bool { return len(s.Get().Missing()) == 0 }

// Locked reports which settings are fixed by the environment.
func (s *Store) Locked() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.locked))
	for k, v := range s.locked {
		out[k] = v
	}
	return out
}

// Merge returns a candidate config: the current process options with the given
// settings applied, except for settings locked by the environment, which keep
// their current values. A blank service-account password keeps the saved one.
func (s *Store) Merge(in Settings) *Config {
	cur := s.Get()
	next := *cur
	next.Settings = in
	// A blank password means "keep the saved one", but only for the same server and account. If the
	// address, encryption or account name changed, the saved password must not follow them: it would be
	// sent to whatever server was typed in.
	sameTarget := strings.EqualFold(strings.TrimSpace(in.LDAP.URL), cur.LDAP.URL) && in.LDAP.StartTLS == cur.LDAP.StartTLS &&
		in.LDAP.SkipVerify == cur.LDAP.SkipVerify && strings.EqualFold(strings.TrimSpace(in.LDAP.BindDN), cur.LDAP.BindDN)
	if in.LDAP.BindPassword == "" && sameTarget {
		next.LDAP.BindPassword = cur.LDAP.BindPassword
	}
	s.mu.RLock()
	lk := s.locked
	keep := func(key string, apply func()) {
		if lk[key] {
			apply()
		}
	}
	keep("ldap.url", func() { next.LDAP.URL = cur.LDAP.URL })
	keep("ldap.startTLS", func() { next.LDAP.StartTLS = cur.LDAP.StartTLS })
	keep("ldap.skipVerify", func() { next.LDAP.SkipVerify = cur.LDAP.SkipVerify })
	keep("ldap.bindDN", func() { next.LDAP.BindDN = cur.LDAP.BindDN })
	keep("ldap.bindPassword", func() { next.LDAP.BindPassword = cur.LDAP.BindPassword })
	keep("users.baseDN", func() { next.Users.BaseDN = cur.Users.BaseDN })
	keep("users.suspendedBaseDN", func() { next.Users.SuspendedBaseDN = cur.Users.SuspendedBaseDN })
	keep("users.loginAttributes", func() { next.Users.LoginAttributes = cur.Users.LoginAttributes })
	keep("groups.baseDN", func() { next.Groups.BaseDN = cur.Groups.BaseDN })
	keep("admin.groupDN", func() { next.Admin.GroupDN = cur.Admin.GroupDN })
	s.mu.RUnlock()
	next.WithDefaults()
	return &next
}

// Save validates and persists the settings in cfg and makes them live.
func (s *Store) Save(cfg *Config) error {
	if m := cfg.Missing(); len(m) > 0 {
		return fmt.Errorf("missing required settings: %s", strings.Join(m, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if s.path != "" {
		b, err := yaml.Marshal(cfg.Settings)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
			return fmt.Errorf("can't create the data directory: %w", err)
		}
		tmp := s.path + ".tmp"
		// The file holds the service account password: owner-only.
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return fmt.Errorf("can't write settings (is the data directory writable?): %w", err)
		}
		if err := os.Rename(tmp, s.path); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}
