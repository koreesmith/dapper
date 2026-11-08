// Package config loads Dapper's settings from a YAML file with environment overrides.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen string `yaml:"listen"`
	Static string `yaml:"static"`
	// DataDir holds settings the admin UI can change (e.g. field policy). Mount it as a volume.
	DataDir string `yaml:"dataDir"`

	LDAP     LDAP     `yaml:"ldap"`
	Users    Users    `yaml:"users"`
	Groups   Groups   `yaml:"groups"`
	Admin    Admin    `yaml:"admin"`
	Password Password `yaml:"password"`
	Session  Session  `yaml:"session"`
}

type LDAP struct {
	URL          string `yaml:"url"`
	StartTLS     bool   `yaml:"startTLS"`
	SkipVerify   bool   `yaml:"skipVerify"`
	BindDN       string `yaml:"bindDN"`       // read-only service account used to look up users
	BindPassword string `yaml:"bindPassword"` // prefer DAPPER_LDAP_BIND_PASSWORD over the file
}

type Users struct {
	BaseDN          string   `yaml:"baseDN"`
	LoginAttributes []string `yaml:"loginAttributes"` // what a user may type to sign in
	ObjectClasses   []string `yaml:"objectClasses"`   // applied to new accounts
	RDNAttribute    string   `yaml:"rdnAttribute"`    // naming attribute for new accounts (default uid)
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

type Password struct {
	MinLength int `yaml:"minLength"` // enforced by Dapper in addition to server policy
}

type Admin struct {
	GroupDN string `yaml:"groupDN"` // members of this group get the admin console
}

type Session struct {
	IdleTimeout  time.Duration `yaml:"idleTimeout"`
	MaxAge       time.Duration `yaml:"maxAge"`
	SecureCookie *bool         `yaml:"secureCookie"` // default: auto (true when served over HTTPS)
}

// Load reads path (if non-empty and present), applies env overrides, defaults, and validates.
func Load(path string) (*Config, error) {
	c := &Config{}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
	}
	c.env()
	c.defaults()
	return c, c.validate()
}

func (c *Config) env() {
	str := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	boolean := func(dst *bool, key string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst, _ = strconv.ParseBool(v)
		}
	}
	str(&c.Listen, "DAPPER_LISTEN")
	str(&c.Static, "DAPPER_STATIC")
	str(&c.DataDir, "DAPPER_DATA_DIR")
	str(&c.Groups.BaseDN, "DAPPER_GROUPS_BASE_DN")
	str(&c.Users.SuspendedBaseDN, "DAPPER_USERS_SUSPENDED_BASE_DN")
	str(&c.LDAP.URL, "DAPPER_LDAP_URL")
	boolean(&c.LDAP.StartTLS, "DAPPER_LDAP_STARTTLS")
	boolean(&c.LDAP.SkipVerify, "DAPPER_LDAP_SKIP_VERIFY")
	str(&c.LDAP.BindDN, "DAPPER_LDAP_BIND_DN")
	str(&c.LDAP.BindPassword, "DAPPER_LDAP_BIND_PASSWORD")
	str(&c.Users.BaseDN, "DAPPER_USERS_BASE_DN")
	if v, ok := os.LookupEnv("DAPPER_USERS_LOGIN_ATTRIBUTES"); ok {
		c.Users.LoginAttributes = strings.Split(v, ",")
	}
	str(&c.Admin.GroupDN, "DAPPER_ADMIN_GROUP_DN")
}

func (c *Config) defaults() {
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
	if c.Groups.ObjectClass == "" {
		c.Groups.ObjectClass = "groupOfNames"
	}
	if c.Groups.MemberAttribute == "" {
		c.Groups.MemberAttribute = "member"
	}
	if c.Password.MinLength == 0 {
		c.Password.MinLength = 8
	}
	if len(c.Users.LoginAttributes) == 0 {
		c.Users.LoginAttributes = []string{"uid", "mail"}
	}
	if c.Session.IdleTimeout == 0 {
		c.Session.IdleTimeout = 30 * time.Minute
	}
	if c.Session.MaxAge == 0 {
		c.Session.MaxAge = 8 * time.Hour
	}
}

func (c *Config) validate() error {
	var missing []string
	for k, v := range map[string]string{
		"ldap.url": c.LDAP.URL, "ldap.bindDN": c.LDAP.BindDN, "ldap.bindPassword": c.LDAP.BindPassword,
		"users.baseDN": c.Users.BaseDN, "admin.groupDN": c.Admin.GroupDN,
	} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required settings: %s", strings.Join(missing, ", "))
	}
	return nil
}
