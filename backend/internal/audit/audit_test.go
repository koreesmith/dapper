package audit

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAddFindAndReload(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.Add(Event{Time: base, Actor: "uid=alice", Action: "login", Target: "uid=alice"})
	l.Add(Event{Time: base.Add(time.Minute), Actor: "uid=alice", Action: "user.create", Target: "uid=bob", Detail: map[string]string{"note": "Welcome Pack"}})
	l.Add(Event{Time: base.Add(2 * time.Minute), Actor: "uid=alice", Action: "group.delete", Target: "cn=staff"})
	l.Close()

	st, err := os.Stat(filepath.Join(dir, "audit.log"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the log must be owner-only: %v %v", st, err)
	}
	l2, err := Open(dir) // survives a restart
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	all, total := l2.Find(Query{})
	if total != 3 || all[0].Action != "group.delete" || all[2].Action != "login" {
		t.Fatalf("newest first after reload: %+v", all)
	}
	if _, n := l2.Find(Query{Category: "user"}); n != 1 {
		t.Errorf("category filter: %d", n)
	}
	if _, n := l2.Find(Query{Text: "welcome pack"}); n != 1 {
		t.Errorf("search reaches detail values, case-insensitively: %d", n)
	}
	if _, n := l2.Find(Query{Text: "bob"}); n != 1 {
		t.Errorf("search reaches the target: %d", n)
	}
	page, total := l2.Find(Query{PageSize: 2, Page: 2})
	if total != 3 || len(page) != 1 || page[0].Action != "login" {
		t.Errorf("paging: %+v total=%d", page, total)
	}
	if got, _ := l2.Find(Query{Page: 99}); len(got) != 0 {
		t.Error("a page past the end is empty")
	}
}

func TestNilLogIsSafeAndRotationKeepsHistory(t *testing.T) {
	var none *Log
	none.Add(Event{Action: "x"})
	if got, n := none.Find(Query{}); got != nil || n != 0 {
		t.Error("nil log")
	}
	dir := t.TempDir()
	l, _ := Open(dir)
	defer l.Close()
	l.size = rotateAt - 10 // force a rotation on the next write
	l.Add(Event{Action: "before"})
	l.Add(Event{Action: "after"})
	if _, err := os.Stat(filepath.Join(dir, "audit.log.1")); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	l.Close()
	l2, _ := Open(dir)
	defer l2.Close()
	if _, n := l2.Find(Query{}); n != 2 {
		t.Errorf("history is kept across a rotation: %d", n)
	}
	_ = strconv.Itoa
}
