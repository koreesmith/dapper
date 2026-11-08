package api

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
)

// ---- field catalog filtered by what's turned on ----

func (s *Server) catalogFor(cfg *config.Config) []policy.Field {
	var out []policy.Field
	for _, f := range s.policy.Fields() {
		switch f.Feature {
		case "unix":
			if !cfg.Unix.Enabled {
				continue
			}
		case "ssh":
			if !cfg.Unix.Enabled || !cfg.Unix.SSHKeys {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

func (s *Server) catalog() []policy.Field { return s.catalogFor(s.c()) }

func (s *Server) field(attr string) (policy.Field, bool) {
	for _, f := range s.catalog() {
		if strings.EqualFold(f.Attr, attr) {
			return f, true
		}
	}
	return policy.Field{}, false
}

func hasObjectClass(e *ldapclient.Entry, oc string) bool {
	for _, v := range e.Values("objectClass") {
		if strings.EqualFold(v, oc) {
			return true
		}
	}
	return false
}

// ---- id allocation ----

var unixMu sync.Mutex // one allocation + create at a time within this process

// suffixOf returns the domain suffix of a DN (from its first dc= component).
func suffixOf(dn string) string {
	for i := 0; i+3 <= len(dn); i++ {
		if (i == 0 || dn[i-1] == ',') && strings.EqualFold(dn[i:i+3], "dc=") {
			return dn[i:]
		}
	}
	return dn
}

// idBases are the places searched for numbers already in use: the whole domain
// if the service account may read it, otherwise just the people and group containers.
func idSearch(c *ldapclient.Client, cfg *config.Config, filter string, attr string) ([]int, error) {
	bases := []string{suffixOf(cfg.Users.BaseDN)}
	var nums []int
	for pass := 0; pass < 2; pass++ {
		nums = nil
		failed := false
		for _, base := range bases {
			es, err := c.SearchComplete(base, filter, []string{attr})
			if err != nil {
				if pass == 0 && (ldapclient.IsCode(err, ldapclient.CodeInsufficientAccess) || ldapclient.IsCode(err, ldapclient.CodeNoSuchObject)) {
					failed = true
					break
				}
				return nil, err
			}
			for _, e := range es {
				for _, v := range e.Values(attr) {
					if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
						nums = append(nums, n)
					}
				}
			}
		}
		if !failed {
			return nums, nil
		}
		bases = []string{cfg.Users.BaseDN, cfg.Groups.BaseDN}
	}
	return nums, nil
}

func idInUse(c *ldapclient.Client, cfg *config.Config, n int) (bool, error) {
	for _, attr := range []string{"uidNumber", "gidNumber"} {
		nums, err := idSearch(c, cfg, fmt.Sprintf("(%s=%d)", attr, n), attr)
		if err != nil {
			return false, err
		}
		if len(nums) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// nextUnixID picks the number after the highest uid or gid already used inside
// the configured range. The search must be complete; if the server cut it short
// we refuse rather than risk reusing a number.
func nextUnixID(c *ldapclient.Client, cfg *config.Config) (int, error) {
	highest := cfg.Unix.UIDMin - 1
	for _, attr := range []string{"uidNumber", "gidNumber"} {
		nums, err := idSearch(c, cfg, "("+attr+"=*)", attr)
		if err != nil {
			return 0, idError(err)
		}
		for _, n := range nums {
			if n >= cfg.Unix.UIDMin && n <= cfg.Unix.UIDMax && n > highest {
				highest = n
			}
		}
	}
	for next := highest + 1; next <= cfg.Unix.UIDMax; next++ {
		used, err := idInUse(c, cfg, next) // belt and braces: never trust a single scan
		if err != nil {
			return 0, idError(err)
		}
		if !used {
			return next, nil
		}
	}
	return 0, validationError(fmt.Sprintf("The Unix ID range %d–%d is full. Widen it under Settings.", cfg.Unix.UIDMin, cfg.Unix.UIDMax))
}

func idError(err error) error {
	if errors.Is(err, ldapclient.ErrIncomplete) {
		return validationError("The directory cut its answer short, so Dapper can't safely choose a new User ID. Raise the server's search size limit for the service account, or enter the UID yourself.")
	}
	return err
}

// ---- creating the Unix parts of a new account ----

func addUnique(list []string, extra ...string) []string {
	have := map[string]bool{}
	for _, v := range list {
		have[strings.ToLower(v)] = true
	}
	out := append([]string(nil), list...)
	for _, e := range extra {
		if !have[strings.ToLower(e)] {
			have[strings.ToLower(e)] = true
			out = append(out, e)
		}
	}
	return out
}

// prepareUnix adds the POSIX attributes to a new account's attributes and returns
// the chosen numeric id (used for the uid and for the private group's gid).
func (s *Server) prepareUnix(c *ldapclient.Client, cfg *config.Config, username string, attrs map[string][]string, override string) (int, error) {
	// Another group already using the username would block the private group.
	es, err := c.SearchAttrs(cfg.Groups.BaseDN, "(cn="+ldapEscape(username)+")", []string{"cn"}, 1)
	if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
		return 0, err
	}
	if len(es) > 0 {
		return 0, validationError("A group named “" + username + "” already exists, so this person's private group can't be created. Pick another username.")
	}

	id, err := s.chooseID(c, cfg, override)
	if err != nil {
		return 0, err
	}

	extra := []string{"posixAccount"}
	if cfg.Unix.Shadow {
		extra = append(extra, "shadowAccount")
	}
	if cfg.Unix.SSHKeys && len(attrs["sshPublicKey"]) > 0 {
		extra = append(extra, "ldapPublicKey")
	}
	attrs["objectClass"] = addUnique(attrs["objectClass"], extra...)
	attrs["uidNumber"] = []string{strconv.Itoa(id)}
	attrs["gidNumber"] = []string{strconv.Itoa(id)}
	if len(attrs["homeDirectory"]) == 0 {
		attrs["homeDirectory"] = []string{strings.ReplaceAll(cfg.Unix.HomePattern, "%u", username)}
	}
	if len(attrs["loginShell"]) == 0 {
		attrs["loginShell"] = []string{cfg.Unix.DefaultShell}
	}
	return id, nil
}

func privateGroupDN(cfg *config.Config, username string) string {
	return "cn=" + escapeRDN(username) + "," + cfg.Groups.BaseDN
}

func addPrivateGroup(c *ldapclient.Client, cfg *config.Config, username string, gid int) error {
	return c.Add(ldapclient.Entry{DN: privateGroupDN(cfg, username), Attrs: map[string][]string{
		"objectClass": {"posixGroup"}, "cn": {username}, "gidNumber": {strconv.Itoa(gid)},
	}})
}

// removePrivateGroup deletes a person's private group, but only if it really is
// theirs: a posixGroup with their name and their gid.
func removePrivateGroup(c *ldapclient.Client, cfg *config.Config, username, gid string) error {
	if username == "" || gid == "" {
		return nil
	}
	dn := privateGroupDN(cfg, username)
	e, err := c.GetAttrs(dn, []string{"objectClass", "gidNumber", "memberUid"})
	if err != nil {
		if ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
			return nil
		}
		return err
	}
	if !hasObjectClass(e, "posixGroup") || first(e.Values("gidNumber")) != gid {
		return nil
	}
	for _, m := range e.Values("memberUid") {
		if !strings.EqualFold(m, username) {
			return nil // other people are in it: it's a shared group that happens to share their name
		}
	}
	return c.Delete(dn)
}

// ---- what exists in the directory already (for the setup wizard) ----

type unixDetection struct {
	Detected     bool     `json:"detected"`
	Accounts     int      `json:"accounts"`
	HighestUID   int      `json:"highestUID"`
	HomePattern  string   `json:"homePattern,omitempty"`
	DefaultShell string   `json:"defaultShell,omitempty"`
	Shells       []string `json:"shells,omitempty"`
}

// detectUnix looks at existing Unix accounts to suggest matching defaults.
func detectUnix(c *ldapclient.Client, base string) unixDetection {
	var d unixDetection
	es, err := c.SearchAttrs(base, "(objectClass=posixAccount)", []string{"uid", "uidNumber", "homeDirectory", "loginShell"}, 5000)
	if err != nil || len(es) == 0 {
		return d
	}
	d.Detected, d.Accounts = true, len(es)
	shells, homes := map[string]int{}, map[string]int{}
	for _, e := range es {
		if n, err := strconv.Atoi(first(e.Values("uidNumber"))); err == nil && n < 60000 && n > d.HighestUID {
			d.HighestUID = n // ignore nobody-style ids such as 65534
		}
		if sh := first(e.Values("loginShell")); sh != "" {
			shells[sh]++
		}
		uid, home := first(e.Values("uid")), first(e.Values("homeDirectory"))
		if uid != "" && strings.HasSuffix(home, "/"+uid) {
			homes[strings.TrimSuffix(home, uid)+"%u"]++
		}
	}
	top := func(m map[string]int) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || m[keys[i]] == m[keys[j]] && keys[i] < keys[j] })
		return keys
	}
	if h := top(homes); len(h) > 0 {
		d.HomePattern = h[0]
	}
	d.Shells = top(shells)
	if len(d.Shells) > 0 {
		d.DefaultShell = d.Shells[0]
		if len(d.Shells) > 6 {
			d.Shells = d.Shells[:6]
		}
	}
	return d
}
