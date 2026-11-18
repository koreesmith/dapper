package reminders

import (
	"testing"
	"time"
)

func at(days float64) (time.Time, time.Time) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	return now, now.Add(time.Duration(days * 24 * float64(time.Hour)))
}

func TestDaysLeftRoundsUp(t *testing.T) {
	for days, want := range map[float64]int{14: 14, 13.5: 14, 7: 7, 0.5: 1, 1: 1, 1.01: 2, 0: 0, -3: -3} {
		now, exp := at(days)
		if got := DaysLeft(now, exp); got != want {
			t.Errorf("%v days: got %d, want %d", days, got, want)
		}
	}
}

func TestLevels(t *testing.T) {
	for left, want := range map[int]int{30: 0, 15: 0, 14: 1, 8: 1, 7: 2, 2: 2, 1: 3, 0: 0, -5: 0} {
		if got := Level(left); got != want {
			t.Errorf("%d days left: level %d, want %d", left, got, want)
		}
	}
}

func TestEachReminderGoesOutOnce(t *testing.T) {
	_, exp := at(14)
	sent := 0
	var got []int
	for _, d := range []float64{20, 14, 13, 10, 7, 6, 3, 1, 0.5} { // a daily check as the expiry nears
		now := exp.Add(-time.Duration(d * 24 * float64(time.Hour)))
		if lvl, send := Due(now, exp, sent); send {
			got = append(got, int(d))
			sent = lvl
		}
	}
	want := []int{14, 7, 1}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("reminders went out at %v days, want %v", got, want)
	}
}

func TestLateArrivalGetsOneReminderNotThree(t *testing.T) {
	now, exp := at(5)
	lvl, send := Due(now, exp, 0)
	if !send || lvl != 2 {
		t.Fatalf("level %d send %v", lvl, send)
	}
	if _, again := Due(now, exp, lvl); again {
		t.Error("the same reminder must not repeat")
	}
}

func TestNothingForExpiredOrFarOff(t *testing.T) {
	for _, d := range []float64{-1, 0, 30} {
		now, exp := at(d)
		if _, send := Due(now, exp, 0); send {
			t.Errorf("%v days: should not send", d)
		}
	}
}

func TestStateSurvivesARestartAndNewExpiryStartsOver(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	_, exp := at(10)
	if err := s.Mark("uid=ann,dc=x", exp, 2); err != nil {
		t.Fatal(err)
	}
	s2 := Open(dir)
	if got := s2.Sent("uid=ann,dc=x", exp); got != 2 {
		t.Errorf("after a restart: %d", got)
	}
	if got := s2.Sent("uid=ann,dc=x", exp.Add(90*24*time.Hour)); got != 0 {
		t.Errorf("a new password (new expiry) must start over, got %d", got)
	}
}

func TestOldEntriesAreForgotten(t *testing.T) {
	s := Open(t.TempDir())
	now, _ := at(0)
	_ = s.Mark("uid=old,dc=x", now.Add(-40*24*time.Hour), 3)
	_ = s.Mark("uid=new,dc=x", now.Add(5*24*time.Hour), 2)
	s.Forget(now)
	if s.Sent("uid=old,dc=x", now.Add(-40*24*time.Hour)) != 0 || s.Sent("uid=new,dc=x", now.Add(5*24*time.Hour)) != 2 {
		t.Error("only the long-expired entry should be dropped")
	}
}
