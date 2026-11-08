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
	c2 := s2.Merge(Settings{LDAP: LDAP{URL: "ldap://y:389", BindDN: "cn=svc"}, Users: Users{BaseDN: "ou=p"}, Admin: Admin{GroupDN: "cn=a"}})
	if c2.LDAP.BindPassword != "secret" || c2.LDAP.URL != "ldap://y:389" {
		t.Fatalf("merge: %+v", c2.LDAP)
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
