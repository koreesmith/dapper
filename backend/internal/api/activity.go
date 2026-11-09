package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"time"

	auditrec "github.com/koreesmith/dapper/backend/internal/audit"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// listAudit serves the Activity page: who did what, newest first, with search and a category filter.
// format=csv downloads every match instead of one page.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, _ *session.Session) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("pageSize"))
	query := auditrec.Query{Text: q.Get("q"), Category: q.Get("category"), Page: page, PageSize: size}
	if q.Get("format") == "csv" {
		query.Page, query.PageSize = 1, 500
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="dapper-activity.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"time", "who", "action", "target", "details"})
		for {
			evs, total := auditLog.Find(query)
			for _, e := range evs {
				d := ""
				for k, v := range e.Detail {
					d += k + "=" + v + "; "
				}
				who := e.Actor
				if who == "-" {
					who = "" // not signed in
				}
				_ = cw.Write([]string{e.Time.Format(time.RFC3339), csvSafe(who), e.Action, csvSafe(e.Target), csvSafe(d)})
			}
			if query.Page*query.PageSize >= total || len(evs) == 0 {
				break
			}
			query.Page++
		}
		cw.Flush()
		return
	}
	evs, total := auditLog.Find(query)
	if evs == nil {
		evs = []auditrec.Event{}
	}
	writeJSON(w, map[string]any{"events": evs, "total": total, "page": query.Page, "pageSize": query.PageSize})
}

// csvSafe stops a spreadsheet treating a cell as a formula (CSV injection): values that start with
// = + - @ get a leading apostrophe.
func csvSafe(v string) string {
	if v != "" && (v[0] == '=' || v[0] == '+' || v[0] == '-' || v[0] == '@') {
		return "'" + v
	}
	return v
}
