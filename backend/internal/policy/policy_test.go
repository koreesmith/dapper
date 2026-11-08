package policy

import "testing"

func TestDefaultsAndUpdate(t *testing.T) {
	dir := t.TempDir()
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := p.Field("mail")
	if !f.UserEditable {
		t.Fatal("mail should be user-editable by default")
	}
	if f, _ := p.Field("givenName"); f.UserEditable {
		t.Fatal("names should not be user-editable by default")
	}
	if f, _ := p.Field("description"); f.UserVisible {
		t.Fatal("notes must be admin-only by default")
	}
	if err := p.Update([]Field{{Attr: "givenName", UserEditable: true}, {Attr: "mail"}}); err != nil {
		t.Fatal(err)
	}
	// persisted across reload
	p2, _ := Load(dir)
	if f, _ := p2.Field("givenName"); !f.UserEditable || !f.UserVisible {
		t.Fatal("override not persisted, or editable did not imply visible")
	}
	if f, _ := p2.Field("mail"); f.UserEditable || f.UserVisible {
		t.Fatal("mail should now be hidden")
	}
	if err := p.Update([]Field{{Attr: "userPassword", UserEditable: true}}); err == nil {
		t.Fatal("must reject attributes outside the catalog")
	}
}

func TestValidate(t *testing.T) {
	mail, _ := lookup("mail")
	tel, _ := lookup("telephoneNumber")
	if Validate(mail, []string{"a@b.org"}) != nil || Validate(mail, []string{"nope"}) == nil {
		t.Error("email validation")
	}
	if Validate(tel, []string{"+1 (555) 010-0100"}) != nil || Validate(tel, []string{"call me"}) == nil {
		t.Error("phone validation")
	}
	if got := Clean([]string{" a ", "", "a", "b"}); len(got) != 2 {
		t.Errorf("clean: %v", got)
	}
}

func TestUnixFieldValidation(t *testing.T) {
	path, _ := lookup("homeDirectory")
	key, _ := lookup("sshPublicKey")
	for _, ok := range []string{"/home/alice", "/bin/bash", "/usr/sbin/nologin"} {
		if Validate(path, []string{ok}) != nil {
			t.Errorf("%q should be a valid path", ok)
		}
	}
	for _, bad := range []string{"home/alice", "/home/a b", "/home/a;rm", ""} {
		if bad != "" && Validate(path, []string{bad}) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	good := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake alice@laptop"
	if Validate(key, []string{good}) != nil {
		t.Error("valid key rejected")
	}
	for _, bad := range []string{"hello world", "ssh-ed25519", "ssh-ed25519 AAAA\ncommand=\"x\" ssh-rsa BBBB"} {
		if Validate(key, []string{bad}) == nil {
			t.Errorf("bad key accepted: %q", bad)
		}
	}
}

func TestReadOnlyFieldsCannotBecomeUserEditable(t *testing.T) {
	p, _ := Load(t.TempDir())
	if err := p.Update([]Field{{Attr: "uidNumber", UserEditable: true}}); err != nil {
		t.Fatal(err)
	}
	if f, _ := p.Field("uidNumber"); f.UserEditable {
		t.Fatal("uidNumber must never be user-editable")
	}
}
