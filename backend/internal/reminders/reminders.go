// Package reminders decides when a person is told their password is about to expire, and remembers what they were
// already told so nobody gets the same reminder twice.
package reminders

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Days are how many days before the expiry a reminder goes out, furthest first.
var Days = []int{14, 7, 1}

// DaysLeft is the number of whole days until expiry, rounded up (23 hours left is "1 day"). Zero or less means it
// has already expired.
func DaysLeft(now, expiry time.Time) int {
	return int(math.Ceil(expiry.Sub(now).Hours() / 24))
}

// Level is how many of the reminder points have been reached: 0 before the first, 3 on the last day.
func Level(daysLeft int) int {
	if daysLeft <= 0 {
		return 0 // already expired: a reminder would be too late to help
	}
	n := 0
	for _, d := range Days {
		if daysLeft <= d {
			n++
		}
	}
	return n
}

// Due says whether a reminder should go out now, given the level already sent for this expiry. Someone first seen
// at 5 days left gets one reminder (the 7-day one), not one for every point they have passed.
func Due(now, expiry time.Time, sent int) (level int, send bool) {
	level = Level(DaysLeft(now, expiry))
	return level, level > sent
}

// State remembers the level sent for each (person, expiry) pair, in a file in the data directory.
type State struct {
	mu   sync.Mutex
	path string
	sent map[string]int
}

func key(dn string, expiry time.Time) string { return dn + "|" + strconv.FormatInt(expiry.Unix(), 10) }

// Open loads the state. A missing or unreadable file starts empty (the worst case is one repeated reminder).
func Open(dataDir string) *State {
	s := &State{sent: map[string]int{}}
	if dataDir != "" {
		s.path = filepath.Join(dataDir, "reminders.json")
		if b, err := os.ReadFile(s.path); err == nil {
			_ = json.Unmarshal(b, &s.sent)
		}
	}
	return s
}

// Sent is the level already sent for this person's current expiry.
func (s *State) Sent(dn string, expiry time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[key(dn, expiry)]
}

// Mark records that the reminder for level was sent.
func (s *State) Mark(dn string, expiry time.Time, level int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[key(dn, expiry)] = level
	return s.save()
}

// Forget drops entries whose expiry is more than a month past, so the file doesn't grow for ever.
func (s *State) Forget(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := now.Add(-30 * 24 * time.Hour).Unix()
	changed := false
	for k := range s.sent {
		if i := lastBar(k); i >= 0 {
			if n, err := strconv.ParseInt(k[i+1:], 10, 64); err == nil && n < cut {
				delete(s.sent, k)
				changed = true
			}
		}
	}
	if changed {
		_ = s.save()
	}
}

func lastBar(k string) int {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '|' {
			return i
		}
	}
	return -1
}

func (s *State) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.sent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return errors.New("can't create the data directory")
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
