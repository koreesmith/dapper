// Package session keeps server-side sessions with idle and absolute timeouts.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

// Session is an authenticated user. Conn is bound as that user, so the
// directory's own ACLs govern what they can do.
type Session struct {
	ID      string
	DN      string
	IsAdmin bool
	Conn    *ldapclient.Client

	created time.Time
	last    time.Time

	// The directory server's configuration connection (cn=config), entered by an admin on the
	// Server page. It lives only in memory, for this session, and expires when idle.
	cfgMu   sync.Mutex
	cfgConn *ldapclient.Client
	cfgUsed time.Time
}

// SetConfigConn stores (replacing and closing any previous) the cn=config connection.
func (s *Session) SetConfigConn(c *ldapclient.Client) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.cfgConn != nil {
		s.cfgConn.Close()
	}
	s.cfgConn, s.cfgUsed = c, time.Now()
}

// ConfigConn returns the cn=config connection if it exists and hasn't been idle longer than maxIdle.
func (s *Session) ConfigConn(maxIdle time.Duration) *ldapclient.Client {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.cfgConn == nil {
		return nil
	}
	if time.Since(s.cfgUsed) > maxIdle {
		s.cfgConn.Close()
		s.cfgConn = nil
		return nil
	}
	s.cfgUsed = time.Now()
	return s.cfgConn
}

// ClearConfigConn closes and forgets the cn=config connection.
func (s *Session) ClearConfigConn() {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	if s.cfgConn != nil {
		s.cfgConn.Close()
		s.cfgConn = nil
	}
}

type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
	idle     time.Duration
	maxAge   time.Duration
}

func NewStore(idle, maxAge time.Duration) *Store {
	s := &Store{sessions: map[string]*Session{}, idle: idle, maxAge: maxAge}
	go s.janitor()
	return s
}

func (s *Store) Create(dn string, admin bool, conn *ldapclient.Client) *Session {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no entropy: refuse to run rather than issue weak IDs
	}
	now := time.Now()
	sess := &Session{ID: hex.EncodeToString(b), DN: dn, IsAdmin: admin, Conn: conn, created: now, last: now}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

// Get returns the live session for id and refreshes its idle timer.
func (s *Store) Get(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return nil
	}
	if s.expired(sess, time.Now()) {
		s.drop(sess)
		return nil
	}
	sess.last = time.Now()
	return sess
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[id]; sess != nil {
		s.drop(sess)
	}
}

func (s *Store) expired(sess *Session, now time.Time) bool {
	return now.Sub(sess.last) > s.idle || now.Sub(sess.created) > s.maxAge
}

func (s *Store) drop(sess *Session) { // caller holds mu
	sess.ClearConfigConn()
	sess.Conn.Close()
	delete(s.sessions, sess.ID)
}

func (s *Store) janitor() {
	for range time.Tick(time.Minute) {
		s.mu.Lock()
		now := time.Now()
		for _, sess := range s.sessions {
			if s.expired(sess, now) {
				s.drop(sess)
			}
		}
		s.mu.Unlock()
	}
}

// SetTimeouts changes the idle and absolute limits for all sessions.
func (s *Store) SetTimeouts(idle, maxAge time.Duration) {
	s.mu.Lock()
	s.idle, s.maxAge = idle, maxAge
	s.mu.Unlock()
}
