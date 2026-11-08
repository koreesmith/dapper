// Package api exposes the HTTP/JSON interface used by the web UI.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/koreesmith/dapper/backend/internal/config"
	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/policy"
	"github.com/koreesmith/dapper/backend/internal/session"
)

const cookieName = "dapper_session"

type Server struct {
	cfg      *config.Config
	policy   *policy.Policy
	sessions *session.Store
	srv      ldapclient.Server
	limit    *limiter
}

func New(cfg *config.Config, pol *policy.Policy) *Server {
	return &Server{
		cfg:      cfg,
		policy:   pol,
		limit:    newLimiter(),
		sessions: session.NewStore(cfg.Session.IdleTimeout, cfg.Session.MaxAge),
		srv:      ldapclient.Server{URL: cfg.LDAP.URL, StartTLS: cfg.LDAP.StartTLS, SkipVerify: cfg.LDAP.SkipVerify},
	}
}

type handler func(http.ResponseWriter, *http.Request, *session.Session)

func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.user(s.me))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]string{"status": "ok"}) })

	// Self-service: any signed-in user, limited to their own account and the field policy.
	mux.HandleFunc("GET /api/me/profile", s.user(s.getProfile))
	mux.HandleFunc("PATCH /api/me/profile", s.user(s.updateProfile))
	mux.HandleFunc("POST /api/me/password", s.user(s.changeOwnPassword))

	// Administration of people, groups and settings.
	mux.HandleFunc("GET /api/admin/users", s.admin(s.listUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.createUser))
	mux.HandleFunc("GET /api/admin/user", s.admin(s.getUser))
	mux.HandleFunc("PATCH /api/admin/user", s.admin(s.updateUser))
	mux.HandleFunc("DELETE /api/admin/user", s.admin(s.deleteUser))
	mux.HandleFunc("POST /api/admin/user/suspend", s.admin(s.setSuspended(true)))
	mux.HandleFunc("POST /api/admin/user/restore", s.admin(s.setSuspended(false)))
	mux.HandleFunc("POST /api/admin/user/password", s.admin(s.resetPassword))
	mux.HandleFunc("GET /api/admin/groups", s.admin(s.listGroups))
	mux.HandleFunc("POST /api/admin/groups", s.admin(s.createGroup))
	mux.HandleFunc("GET /api/admin/group", s.admin(s.getGroup))
	mux.HandleFunc("DELETE /api/admin/group", s.admin(s.deleteGroup))
	mux.HandleFunc("POST /api/admin/group/members", s.admin(s.changeMember(true)))
	mux.HandleFunc("DELETE /api/admin/group/members", s.admin(s.changeMember(false)))
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.getSettings))
	mux.HandleFunc("PUT /api/admin/settings/fields", s.admin(s.putFieldPolicy))

	// Admin-only directory access. Enforced here, not just hidden in the UI.
	mux.HandleFunc("GET /api/admin/children", s.admin(s.children))
	mux.HandleFunc("GET /api/admin/entry", s.admin(s.getEntry))
	mux.HandleFunc("GET /api/admin/search", s.admin(s.search))
	mux.HandleFunc("POST /api/admin/entry", s.admin(s.addEntry))
	mux.HandleFunc("PATCH /api/admin/entry", s.admin(s.modifyEntry))
	mux.HandleFunc("DELETE /api/admin/entry", s.admin(s.deleteEntry))

	if static != nil {
		mux.Handle("/", static)
	}
	return secure(mux)
}

// secure adds security headers and a CSRF guard: state-changing requests must
// carry a custom header that a cross-site form cannot set.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "dapper" {
			fail(w, http.StatusForbidden, "forbidden")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) user(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess := s.sessions.Get(c.Value)
		if sess == nil {
			fail(w, http.StatusUnauthorized, "not signed in")
			return
		}
		h(w, r, sess)
	}
}

func (s *Server) admin(h handler) http.HandlerFunc {
	return s.user(func(w http.ResponseWriter, r *http.Request, sess *session.Session) {
		if !sess.IsAdmin {
			fail(w, http.StatusForbidden, "administrator access required")
			return
		}
		h(w, r, sess)
	})
}

const loginFailed = "Incorrect username or password."

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username == "" || in.Password == "" {
		fail(w, http.StatusUnauthorized, loginFailed)
		return
	}
	in.Username = strings.TrimSpace(in.Username)

	ipKey, userKey := "ip:"+clientIP(r), "user:"+strings.ToLower(in.Username)
	if s.limit.blocked(ipKey, userKey) {
		w.Header().Set("Retry-After", "600")
		fail(w, http.StatusTooManyRequests, "Too many failed sign-in attempts. Wait a few minutes and try again.")
		return
	}
	failed := func() {
		s.limit.fail(ipKey, userKey)
		fail(w, http.StatusUnauthorized, loginFailed)
	}

	// 1. Look up the user's DN and admin status with the service account.
	svc, err := ldapclient.Dial(s.srv)
	if err != nil {
		log.Printf("login: %v", err)
		fail(w, http.StatusBadGateway, "The directory server is unavailable. Try again shortly.")
		return
	}
	defer svc.Close()
	if err := svc.Bind(s.cfg.LDAP.BindDN, s.cfg.LDAP.BindPassword); err != nil {
		log.Printf("login: service account bind failed: %v", err)
		fail(w, http.StatusBadGateway, "Dapper cannot reach the directory (service account problem). Contact your administrator.")
		return
	}
	dn, err := svc.FindUserDN(s.cfg.Users.BaseDN, s.cfg.Users.LoginAttributes, in.Username)
	if err != nil {
		if !errors.Is(err, ldapclient.ErrInvalidCredentials) {
			log.Printf("login: user lookup: %v", err)
		}
		failed()
		return
	}
	isAdmin, err := svc.IsMember(s.cfg.Admin.GroupDN, dn)
	if err != nil {
		log.Printf("login: admin group check: %v", err)
		fail(w, http.StatusBadGateway, "Could not determine your access level. Contact your administrator.")
		return
	}

	// 2. Verify the password by binding as the user on a fresh connection,
	// which is then kept for the session so the directory's ACLs apply.
	conn, err := ldapclient.Dial(s.srv)
	if err != nil {
		fail(w, http.StatusBadGateway, "The directory server is unavailable. Try again shortly.")
		return
	}
	if err := conn.Bind(dn, in.Password); err != nil {
		conn.Close()
		if !errors.Is(err, ldapclient.ErrInvalidCredentials) {
			log.Printf("login: user bind: %v", err)
		}
		failed()
		return
	}

	s.limit.reset(ipKey, userKey)
	sess := s.sessions.Create(dn, isAdmin, conn)
	audit(sess, "login", dn)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: sess.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure:  s.secureCookie(r),
		Expires: time.Now().Add(s.cfg.Session.MaxAge),
	})
	s.me(w, r, sess)
}

func (s *Server) secureCookie(r *http.Request) bool {
	if s.cfg.Session.SecureCookie != nil {
		return *s.cfg.Session.SecureCookie
	}
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	out := map[string]any{
		"dn": sess.DN, "isAdmin": sess.IsAdmin, "name": sess.DN,
		"features": map[string]any{
			"groups":            s.groupsEnabled(),
			"suspendByMove":     s.cfg.Users.SuspendedBaseDN != "",
			"passwordMinLength": s.cfg.Password.MinLength,
		},
	}
	if e, err := sess.Conn.Get(sess.DN); err == nil {
		for _, a := range []string{"displayName", "cn", "uid"} {
			if v := e.Attrs[a]; len(v) > 0 {
				out["name"] = v[0]
				break
			}
		}
	}
	writeJSON(w, out)
}

func (s *Server) children(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn := r.URL.Query().Get("dn")
	if dn == "" {
		dn = s.cfg.Users.BaseDN
	}
	res, err := sess.Conn.Children(dn)
	reply(w, res, err)
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	res, err := sess.Conn.Get(r.URL.Query().Get("dn"))
	reply(w, res, err)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	q := r.URL.Query()
	base := q.Get("base")
	if base == "" {
		base = s.cfg.Users.BaseDN
	}
	res, err := sess.Conn.Search(base, q.Get("filter"), 200)
	reply(w, res, err)
}

func (s *Server) addEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var e ldapclient.Entry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reply(w, map[string]string{"dn": e.DN}, sess.Conn.Add(e))
}

func (s *Server) modifyEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var ch []ldapclient.Change
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reply(w, map[string]string{"status": "ok"}, sess.Conn.Modify(r.URL.Query().Get("dn"), ch))
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	reply(w, map[string]string{"status": "ok"}, sess.Conn.Delete(r.URL.Query().Get("dn")))
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
