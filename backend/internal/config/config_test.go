package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSaveReloadAndLocking(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DAPPER_DATA_DIR", dir)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if s.Configured() {
		t.Fatal("a blank install must be unconfigured, not an error")
	}
	cfg := s.Merge(Settings{
		LDAP:  LDAP{URL: "ldap://x:389", BindDN: "cn=svc", BindPassword: "secret"},
		Users: Users{BaseDN: "ou=people,dc=x"}, Admin: Admin{GroupDN: "cn=admins,dc=x"},
	})
	if err := s.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if !s.Configured() {
		t.Fatal("should be configured after save")
	}
	st, err := os.Stat(filepath.Join(dir, "settings.yaml"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("settings file must be owner-only: %v %v", st, err)
	}

	// Reload from disk.
	s2, err := Open("")
	if err != nil || !s2.Configured() || s2.Get().LDAP.BindPassword != "secret" {
		t.Fatalf("reload: %v %+v", err, s2.Get().LDAP)
	}
	// A blank password on update keeps the saved one.
	c2 := s2.Merge(Settings{LDAP: LDAP{URL: "ldap://x:389", BindDN: "cn=svc"}, Users: Users{BaseDN: "ou=p"}, Admin: Admin{GroupDN: "cn=a"}})
	if c2.LDAP.BindPassword != "secret" {
		t.Fatalf("an unchanged server keeps the saved password: %+v", c2.LDAP)
	}
	// ...but a different server never inherits it (see TestSavedPasswordDoesNotFollowADifferentServer).
	if c3 := s2.Merge(Settings{LDAP: LDAP{URL: "ldap://y:389", BindDN: "cn=svc"}}); c3.LDAP.BindPassword != "" || c3.LDAP.URL != "ldap://y:389" {
		t.Fatalf("a changed server must not inherit the password: %+v", c3.LDAP)
	}

	// Environment variables win and lock the setting.
	t.Setenv("DAPPER_LDAP_URL", "ldap://env:389")
	s3, _ := Open("")
	if s3.Get().LDAP.URL != "ldap://env:389" || !s3.Locked()["ldap.url"] {
		t.Fatal("env should override and lock")
	}
	c3 := s3.Merge(Settings{LDAP: LDAP{URL: "ldap://ui:389"}})
	if c3.LDAP.URL != "ldap://env:389" {
		t.Fatal("UI must not be able to change a locked setting")
	}
}

func TestValidate(t *testing.T) {
	c := &Config{}
	c.WithDefaults()
	c.LDAP.URL = "http://nope"
	if c.Validate() == nil {
		t.Error("non-ldap URL accepted")
	}
	c.LDAP.URL = "ldaps://ok"
	c.Session.IdleTimeout, c.Session.MaxAge = 2*60*1e9, 60*1e9
	if c.Validate() == nil {
		t.Error("idle timeout longer than max age accepted")
	}
}

func TestUnixValidation(t *testing.T) {
	ok := func() *Config {
		c := &Config{}
		c.WithDefaults()
		c.Groups.BaseDN = "ou=groups,dc=x"
		c.Unix.Enabled = true
		return c
	}
	if err := ok().Validate(); err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}
	cases := map[string]func(*Config){
		"needs a groups container": func(c *Config) { c.Groups.BaseDN = "" },
		"backwards range":          func(c *Config) { c.Unix.UIDMin, c.Unix.UIDMax = 20000, 10000 },
		"zero start":               func(c *Config) { c.Unix.UIDMin = -1 },
		"relative home":            func(c *Config) { c.Unix.HomePattern = "home/%u" },
		"home with a space":        func(c *Config) { c.Unix.HomePattern = "/home/my users/%u" },
		"relative shell":           func(c *Config) { c.Unix.DefaultShell = "bash" },
	}
	for name, mutate := range cases {
		c := ok()
		mutate(c)
		if c.Validate() == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	off := ok()
	off.Unix.Enabled, off.Groups.BaseDN = false, ""
	if off.Validate() != nil {
		t.Error("Unix off shouldn't need a groups container")
	}
}

func TestSavedPasswordDoesNotFollowADifferentServer(t *testing.T) {
	c := &Config{}
	c.LDAP = LDAP{URL: "ldaps://ldap.example.org", BindDN: "cn=svc", BindPassword: "secret"}
	c.Users.BaseDN, c.Admin.GroupDN = "ou=p,dc=x", "cn=a,dc=x"
	s := Static(c)
	same := Settings{LDAP: LDAP{URL: "ldaps://ldap.example.org", BindDN: "cn=svc"}}
	if got := s.Merge(same).LDAP.BindPassword; got != "secret" {
		t.Fatalf("an unchanged target keeps the saved password, got %q", got)
	}
	for name, mod := range map[string]func(*Settings){
		"address":    func(x *Settings) { x.LDAP.URL = "ldap://attacker.example:389" },
		"account":    func(x *Settings) { x.LDAP.BindDN = "cn=other" },
		"starttls":   func(x *Settings) { x.LDAP.StartTLS = true },
		"skipVerify": func(x *Settings) { x.LDAP.SkipVerify = true },
	} {
		in := same
		mod(&in)
		if got := s.Merge(in).LDAP.BindPassword; got != "" {
			t.Errorf("changing the %s must not carry the saved password along, got %q", name, got)
		}
	}
	// Typing a password is always honoured.
	in := same
	in.LDAP.URL, in.LDAP.BindPassword = "ldap://new:389", "typed"
	if got := s.Merge(in).LDAP.BindPassword; got != "typed" {
		t.Errorf("typed password ignored: %q", got)
	}
}
