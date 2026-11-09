// Package audit keeps a persistent record of who did what in Dapper: sign-ins and every
// administrative change. It is append-only JSON lines in the data directory (owner-only),
// with the most recent events held in memory for the Activity page. Passwords and other
// secrets are never recorded.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Time   time.Time         `json:"time"`
	Actor  string            `json:"actor"`
	Action string            `json:"action"`
	Target string            `json:"target,omitempty"`
	Detail map[string]string `json:"detail,omitempty"`
}

const (
	keepInMemory = 20000            // events available on the Activity page
	rotateAt     = 10 * 1024 * 1024 // start a new file after 10 MB; the previous one is kept as audit.log.1
)

type Log struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	size   int64
	events []Event // oldest first
}

// Open opens (creating if needed) <dir>/audit.log and loads the most recent events.
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	l := &Log{path: filepath.Join(dir, "audit.log")}
	for _, p := range []string{l.path + ".1", l.path} { // older file first
		l.load(p)
	}
	if len(l.events) > keepInMemory {
		l.events = l.events[len(l.events)-keepInMemory:]
	}
	if err := l.openFile(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Log) load(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && !e.Time.IsZero() {
			l.events = append(l.events, e)
		}
	}
}

func (l *Log) openFile() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	if st != nil {
		l.size = st.Size()
	}
	l.file = f
	return nil
}

// Add records an event. A nil *Log is a no-op, so callers needn't check.
func (l *Log) Add(e Event) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	if len(l.events) > keepInMemory+1000 {
		l.events = append([]Event(nil), l.events[len(l.events)-keepInMemory:]...)
	}
	b, err := json.Marshal(e)
	if err != nil || l.file == nil {
		return
	}
	b = append(b, '\n')
	if l.size+int64(len(b)) > rotateAt {
		l.file.Close()
		_ = os.Rename(l.path, l.path+".1")
		if l.openFile() != nil {
			return
		}
	}
	n, _ := l.file.Write(b)
	l.size += int64(n)
}

// Query selects events, newest first.
type Query struct {
	Text     string // matches the actor, action, target or any detail value
	Category string // action prefix such as "user" or "group"; "" for all
	Page     int
	PageSize int
}

func (e Event) matches(q Query) bool {
	if q.Category != "" && !strings.HasPrefix(e.Action, q.Category) {
		return false
	}
	if q.Text == "" {
		return true
	}
	t := strings.ToLower(q.Text)
	if strings.Contains(strings.ToLower(e.Actor+" "+e.Action+" "+e.Target), t) {
		return true
	}
	for _, v := range e.Detail {
		if strings.Contains(strings.ToLower(v), t) {
			return true
		}
	}
	return false
}

// Find returns one page of matching events and the total number of matches.
func (l *Log) Find(q Query) ([]Event, int) {
	if l == nil {
		return nil, 0
	}
	if q.PageSize < 1 || q.PageSize > 500 {
		q.PageSize = 50
	}
	if q.Page < 1 {
		q.Page = 1
	}
	l.mu.Lock()
	var hits []Event
	for i := len(l.events) - 1; i >= 0; i-- {
		if l.events[i].matches(q) {
			hits = append(hits, l.events[i])
		}
	}
	l.mu.Unlock()
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Time.After(hits[j].Time) })
	total := len(hits)
	start := (q.Page - 1) * q.PageSize
	if start > total {
		start = total
	}
	end := start + q.PageSize
	if end > total {
		end = total
	}
	return hits[start:end], total
}

// Close flushes and closes the file.
func (l *Log) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
