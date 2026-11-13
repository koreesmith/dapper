package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/koreesmith/dapper/backend/internal/session"
)

// Backups of a Dapper-run directory server. The server image makes them (on a schedule, and when asked) into a
// volume shared with Dapper; Dapper lists them, asks for one by creating a request file, and offers downloads.

var dirBackupRE = regexp.MustCompile(`^dapper-ldap-(scheduled|manual|pre-restore)-(\d{8}-\d{6})\.tar\.gz$`)

func backupDir() string { return envOr("DAPPER_BACKUP_DIR", "/backups") }

// dapperRuns reports whether the directory server is one Dapper set up and runs. Setup records the server's
// public name, but a server set up by an earlier build has no name saved; the directory server image also leaves
// a status file in the shared backups folder, which only a Dapper-run server does.
func (s *Server) dapperRuns() bool {
	cfg := s.c()
	if cfg.LDAP.PublicHost != "" {
		return true
	}
	// A Dapper-run server may be attached beside the one Dapper manages (while migrating), so the status file
	// only counts when Dapper's own connection is to that server.
	if !strings.EqualFold(strings.TrimRight(cfg.LDAP.URL, "/"), strings.TrimRight(bundledURL(), "/")) {
		return false
	}
	_, err := os.Stat(filepath.Join(backupDir(), "status.json"))
	return err == nil
}

type backupInfo struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Time string `json:"time"` // UTC, RFC 3339
	Size int64  `json:"size"`
}

// backupStatus is what the server image's backup loop writes to status.json.
type backupStatus struct {
	LastAttempt   int64  `json:"lastAttempt"`
	LastSuccess   string `json:"lastSuccess"`
	LastError     string `json:"lastError"`
	IntervalHours int    `json:"intervalHours"`
	Keep          int    `json:"keep"`
	DataBytes     int64  `json:"dataBytes"`
	DiskFreeBytes int64  `json:"diskFreeBytes"`
	Image         string `json:"image"`
}

func (s *Server) backupsGet(w http.ResponseWriter, _ *http.Request, _ *session.Session) {
	dir := backupDir()
	if !s.dapperRuns() {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, map[string]any{"enabled": false, "message": "Dapper can't see the server's backup folder. Check that the backups volume is shared with Dapper (see docker-compose.server.yml)."})
		return
	}
	list := []backupInfo{}
	for _, e := range es {
		m := dirBackupRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		t, err := time.Parse("20060102-150405", m[2])
		fi, ferr := e.Info()
		if err != nil || ferr != nil {
			continue
		}
		list = append(list, backupInfo{Name: e.Name(), Kind: m[1], Time: t.UTC().Format(time.RFC3339), Size: fi.Size()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Time > list[j].Time })

	var st backupStatus
	if b, err := os.ReadFile(filepath.Join(dir, "status.json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	_, reqErr := os.Stat(filepath.Join(dir, "request"))

	// A plain-words problem, if there is one: no backup, an old one, or a failing one.
	problem := ""
	var newest time.Time
	for _, b := range list {
		if b.Kind != "pre-restore" {
			newest, _ = time.Parse(time.RFC3339, b.Time)
			break
		}
	}
	switch {
	case st.LastError != "":
		problem = "The last backup failed: " + st.LastError
	case newest.IsZero():
		problem = "There is no backup yet."
	case st.IntervalHours > 0 && time.Since(newest) > time.Duration(2*st.IntervalHours)*time.Hour:
		problem = "The newest backup is more than twice as old as the schedule says it should be."
	case st.IntervalHours == 0 && time.Since(newest) > 7*24*time.Hour:
		problem = "Scheduled backups are off and the newest backup is over a week old."
	}
	writeJSON(w, map[string]any{
		"enabled": true, "backups": list, "status": st, "problem": problem, "requested": reqErr == nil,
	})
}

// backupsCreate asks the server to make a backup now.
func (s *Server) backupsCreate(w http.ResponseWriter, _ *http.Request, sess *session.Session) {
	if !s.dapperRuns() {
		fail(w, http.StatusConflict, "This directory server isn't run by Dapper.")
		return
	}
	f, err := os.OpenFile(filepath.Join(backupDir(), "request"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
	if os.IsExist(err) {
		writeJSONStatus(w, http.StatusAccepted, map[string]bool{"requested": true})
		return
	}
	if err != nil {
		fail(w, http.StatusBadGateway, "Dapper couldn't ask the server for a backup (is the backups volume shared with Dapper and writable?): "+err.Error()+".")
		return
	}
	f.Close()
	audit(sess, "backup.request", "now")
	writeJSONStatus(w, http.StatusAccepted, map[string]bool{"requested": true})
}

// backupsDownload sends a backup file. A backup holds every password hash, so it needs the server settings to
// be unlocked (the separate configuration login) as well as an administrator.
func (s *Server) backupsDownload(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if sess.ConfigConn(configIdle) == nil {
		fail(w, http.StatusForbidden, "Backups contain every password hash, so downloading one needs the server settings unlocked. Unlock them below and try again.")
		return
	}
	name := r.URL.Query().Get("name")
	if !dirBackupRE.MatchString(name) {
		fail(w, http.StatusBadRequest, "Unknown backup.")
		return
	}
	f, err := os.Open(filepath.Join(backupDir(), name))
	if err != nil {
		fail(w, http.StatusNotFound, "That backup isn't there any more.")
		return
	}
	defer f.Close()
	audit(sess, "backup.download", name)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	fi, _ := f.Stat()
	http.ServeContent(w, r, name, fi.ModTime(), f)
}
