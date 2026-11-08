// Package api exposes the HTTP/JSON interface used by the web UI.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

const cookieName = "dapper_session"

type Server struct {
	mu       sync.Mutex
	sessions map[string]*ldapclient.Client
}

func New() *Server { return &Server{sessions: map[string]*ldapclient.Client{}} }

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/connect", s.connect)
	mux.HandleFunc("POST /api/disconnect", s.disconnect)
	mux.HandleFunc("GET /api/session", s.auth(s.session))
	mux.HandleFunc("GET /api/children", s.auth(s.children))
	mux.HandleFunc("GET /api/entry", s.auth(s.getEntry))
	mux.HandleFunc("GET /api/search", s.auth(s.search))
	mux.HandleFunc("POST /api/entry", s.auth(s.addEntry))
	mux.HandleFunc("PATCH /api/entry", s.auth(s.modifyEntry))
	mux.HandleFunc("DELETE /api/entry", s.auth(s.deleteEntry))
	if static != nil {
		mux.Handle("/", static)
	}
	return mux
}

type handler func(http.ResponseWriter, *http.Request, *ldapclient.Client)

func (s *Server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, http.StatusUnauthorized, "not connected")
			return
		}
		s.mu.Lock()
		cl := s.sessions[c.Value]
		s.mu.Unlock()
		if cl == nil {
			fail(w, http.StatusUnauthorized, "not connected")
			return
		}
		h(w, r, cl)
	}
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var cfg ldapclient.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cl, err := ldapclient.Connect(cfg)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[id] = cl
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, map[string]string{"baseDN": cfg.BaseDN})
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		if cl := s.sessions[c.Value]; cl != nil {
			cl.Close()
			delete(s.sessions, c.Value)
		}
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request, cl *ldapclient.Client) {
	writeJSON(w, map[string]string{"baseDN": cl.BaseDN()})
}

func (s *Server) children(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	dn := r.URL.Query().Get("dn")
	if dn == "" {
		dn = cl.BaseDN()
	}
	res, err := cl.Children(dn)
	reply(w, res, err)
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	res, err := cl.Get(r.URL.Query().Get("dn"))
	reply(w, res, err)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	q := r.URL.Query()
	base := q.Get("base")
	if base == "" {
		base = cl.BaseDN()
	}
	res, err := cl.Search(base, q.Get("filter"), 200)
	reply(w, res, err)
}

func (s *Server) addEntry(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	var e ldapclient.Entry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reply(w, map[string]string{"dn": e.DN}, cl.Add(e))
}

func (s *Server) modifyEntry(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	var ch []ldapclient.Change
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reply(w, map[string]string{"status": "ok"}, cl.Modify(r.URL.Query().Get("dn"), ch))
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request, cl *ldapclient.Client) {
	reply(w, map[string]string{"status": "ok"}, cl.Delete(r.URL.Query().Get("dn")))
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
