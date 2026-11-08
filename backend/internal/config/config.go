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

	LDAP    LDAP    `yaml:"ldap"`
	Users   Users   `yaml:"users"`
	Admin   Admin   `yaml:"admin"`
	Session Session `yaml:"session"`
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
