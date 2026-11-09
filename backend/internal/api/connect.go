package api

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/koreesmith/dapper/backend/internal/session"
)

// firstCertificate reads the first certificate from PEM text.
func firstCertificate(text string) (*x509.Certificate, error) {
	rest := []byte(text)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return nil, validationError("That doesn't look like a certificate. Paste the whole thing, including the “BEGIN CERTIFICATE” and “END CERTIFICATE” lines.")
		}
		if b.Type == "CERTIFICATE" {
			return x509.ParseCertificate(b.Bytes)
		}
	}
}

// caGet describes the certificate Dapper trusts for the directory, if one was set.
func (s *Server) caGet(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	path := s.c().LDAP.CAFile
	if path == "" {
		writeJSON(w, map[string]any{"set": false})
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, map[string]any{"set": true, "error": "Dapper can't read its trusted certificate file."})
		return
	}
	c, err := firstCertificate(string(b))
	if err != nil {
		writeJSON(w, map[string]any{"set": true, "error": "The trusted certificate file doesn't contain a certificate."})
		return
	}
	writeJSON(w, map[string]any{"set": true, "cert": describeCert(c), "pem": string(b)})
}

// caPut replaces the trusted certificate. It is tried against the directory first, so a wrong one is
// refused instead of cutting Dapper off from its directory.
func (s *Server) caPut(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct{ PEM string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.PEM = strings.TrimSpace(in.PEM) + "\n"
	if _, err := firstCertificate(in.PEM); err != nil {
		failLDAP(w, err)
		return
	}
	cfg := *s.c()
	dir := cfg.DataDir
	trial := filepath.Join(dir, "ldap-ca.pem.try")
	if err := os.WriteFile(trial, []byte(in.PEM), 0o600); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the certificate: "+err.Error()+".")
		return
	}
	defer os.Remove(trial)
	try := cfg
	try.LDAP.CAFile = trial
	c, err := dialSvc(&try)
	if err != nil {
		fail(w, http.StatusBadRequest, "Dapper couldn't connect to the directory with that certificate, so it was not saved. Check that it is the certificate (or its issuer's) the server presents. Details: "+err.Error()+".")
		return
	}
	c.Close()

	final := filepath.Join(dir, "ldap-ca.pem")
	if err := os.Rename(trial, final); err != nil {
		fail(w, http.StatusInternalServerError, "Couldn't save the certificate: "+err.Error()+".")
		return
	}
	if cfg.LDAP.CAFile != final {
		cfg.LDAP.CAFile = final
		if err := s.store.Save(&cfg); err != nil {
			fail(w, http.StatusInternalServerError, "Couldn't save the settings: "+err.Error()+".")
			return
		}
	}
	audit(sess, "settings.ca.update", final)
	s.caGet(w, r, sess)
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// connectGet gives what machines need to use a Dapper-run directory server.
func (s *Server) connectGet(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	cfg := s.c()
	if cfg.LDAP.PublicHost == "" {
		writeJSON(w, map[string]any{"dapperRun": false})
		return
	}
	out := map[string]any{
		"dapperRun": true, "host": cfg.LDAP.PublicHost, "suffix": suffixOf(cfg.Users.BaseDN),
		"ldapPort": envOr("LDAP_PORT", "389"), "ldapsPort": envOr("LDAPS_PORT", "636"),
	}
	if b, err := os.ReadFile(cfg.LDAP.CAFile); err == nil {
		out["certificate"] = string(b)
		if c, err := firstCertificate(string(b)); err == nil {
			out["cert"] = describeCert(c)
		}
	}
	writeJSON(w, out)
}
