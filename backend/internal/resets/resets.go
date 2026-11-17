// Package resets holds the one-time links that let someone who forgot their password choose a new one.
//
// A link carries a random token. Only the token's SHA-256 is kept, so reading Dapper's memory or a core dump
// doesn't hand out working links. Tokens live in memory only: restarting Dapper invalidates every outstanding
// link, which is the safe direction to fail in. Each account has at most one live link (a new request replaces
// the old one), and a link works once.
package resets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
	"time"
)

// TTL is how long a link stays valid.
const TTL = time.Hour

type entry struct {
	dn      string
	expires time.Time
}

// Store is the set of live links.
type Store struct {
	mu    sync.Mutex
	byTok map[[32]byte]entry
	byDN  map[string][32]byte
	now   func() time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{byTok: map[[32]byte]entry{}, byDN: map[string][32]byte{}, now: time.Now}
}

func hash(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func key(dn string) string { return strings.ToLower(strings.TrimSpace(dn)) }

// prune drops expired links. The caller holds the lock.
func (s *Store) prune() {
	t := s.now()
	for h, e := range s.byTok {
		if !t.Before(e.expires) {
			delete(s.byTok, h)
			if s.byDN[key(e.dn)] == h {
				delete(s.byDN, key(e.dn))
			}
		}
	}
}

// Issue makes a new link for the account, replacing any earlier one, and returns the token to put in the link.
func (s *Store) Issue(dn string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if old, ok := s.byDN[key(dn)]; ok {
		delete(s.byTok, old)
	}
	h := hash(tok)
	s.byTok[h] = entry{dn: dn, expires: s.now().Add(TTL)}
	s.byDN[key(dn)] = h
	return tok, nil
}

// Lookup says which account a token is for, without using it up.
func (s *Store) Lookup(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	e, ok := s.byTok[hash(token)]
	return e.dn, ok
}

// Consume uses the token up and returns its account. A second use fails.
func (s *Store) Consume(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	h := hash(token)
	e, ok := s.byTok[h]
	if !ok {
		return "", false
	}
	delete(s.byTok, h)
	if s.byDN[key(e.dn)] == h {
		delete(s.byDN, key(e.dn))
	}
	return e.dn, true
}

// Revoke drops any link for the account, for example once its password has changed some other way.
func (s *Store) Revoke(dn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.byDN[key(dn)]; ok {
		delete(s.byTok, h)
		delete(s.byDN, key(dn))
	}
}
