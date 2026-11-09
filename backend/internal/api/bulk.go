package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
	"github.com/koreesmith/dapper/backend/internal/users"
)

const maxImportRows = 500
const maxBulk = 200

// ---- export ----

// writeUsersCSV streams everyone in all as CSV. Cells that a spreadsheet could treat as a formula are neutralised.
func writeUsersCSV(w http.ResponseWriter, all []users.User) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="dapper-people.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"username", "first name", "last name", "display name", "email", "phone", "title", "department", "status"})
	for _, u := range all {
		status := "active"
		if u.Suspended {
			status = "suspended"
		}
		_ = cw.Write([]string{csvSafe(u.Username), csvSafe(u.GivenName), csvSafe(u.Surname), csvSafe(u.Name), csvSafe(u.Email),
			csvSafe(strings.Join(u.Phones, "; ")), csvSafe(u.Title), csvSafe(u.Department), status})
	}
	cw.Flush()
}

// ---- import ----

var importColumns = map[string][]string{
	"username":         {"username", "user", "login", "uid", "user name"},
	"givenName":        {"first name", "firstname", "given name", "givenname", "first", "forename"},
	"sn":               {"last name", "lastname", "surname", "sn", "last", "family name"},
	"mail":             {"email", "e-mail", "mail", "email address"},
	"telephoneNumber":  {"phone", "telephone", "telephonenumber", "work phone"},
	"mobile":           {"mobile", "cell", "mobile phone", "cell phone"},
	"title":            {"title", "job title"},
	"departmentNumber": {"department", "dept"},
	"password":         {"password", "initial password"},
}

type importRow struct {
	Row      int    `json:"row"` // the row's line number in the file (the header is row 1)
	Username string `json:"username"`
	Name     string `json:"name"`
	Status   string `json:"status"` // ready, created, error
	Message  string `json:"message,omitempty"`
	Temp     string `json:"temporaryPassword,omitempty"`
}

// parseImport reads the CSV into people. Problems with the file as a whole are returned as an error; problems with one
// row are put on that row.
func (s *Server) parseImport(text string, groups []string) ([]newPerson, []importRow, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\xef\xbb\xbf")))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, validationError("That doesn't look like a CSV file: " + err.Error())
	}
	if len(recs) < 2 {
		return nil, nil, validationError("The file needs a header row and at least one person.")
	}
	if len(recs)-1 > maxImportRows {
		return nil, nil, validationError("Import up to 500 people at a time; this file has " + itoa(len(recs)-1) + ". Split it into smaller files.")
	}
	col := map[string]int{}
	for i, h := range recs[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		for field, names := range importColumns {
			for _, n := range names {
				if h == n {
					if _, dup := col[field]; !dup {
						col[field] = i
					}
				}
			}
		}
	}
	if _, ok := col["givenName"]; !ok {
		return nil, nil, validationError("Couldn't find a “first name” column. Expected columns include: first name, last name, email, phone, username (optional).")
	}
	if _, ok := col["sn"]; !ok {
		return nil, nil, validationError("Couldn't find a “last name” column. Expected columns include: first name, last name, email, phone, username (optional).")
	}
	get := func(rec []string, field string) string {
		if i, ok := col[field]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var people []newPerson
	var rows []importRow
	for i, rec := range recs[1:] {
		g, sn := get(rec, "givenName"), get(rec, "sn")
		username := strings.ToLower(get(rec, "username"))
		if username == "" {
			username = suggestUsername(g, sn)
		}
		attrs := map[string][]string{"givenName": {g}, "sn": {sn}}
		for _, f := range []string{"mail", "telephoneNumber", "mobile", "title", "departmentNumber"} {
			if v := get(rec, f); v != "" {
				attrs[f] = []string{v}
			}
		}
		people = append(people, newPerson{Username: username, Attrs: attrs, Password: get(rec, "password"), Groups: groups})
		rows = append(rows, importRow{Row: i + 2, Username: username, Name: strings.TrimSpace(g + " " + sn), Status: "ready"})
	}
	return people, rows, nil
}

// suggestUsername builds first.last from a name, keeping only characters usernames may contain.
func suggestUsername(first, last string) string {
	clean := func(v string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(v) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	f, l := clean(first), clean(last)
	if f == "" || l == "" {
		return f + l
	}
	return f + "." + l
}

func (s *Server) importUsers(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		CSV    string   `json:"csv"`
		DryRun bool     `json:"dryRun"`
		Groups []string `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	cfg := s.c()
	people, rows, err := s.parseImport(in.CSV, in.Groups)
	if err != nil {
		failLDAP(w, err)
		return
	}
	created, failed := 0, 0
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		// Who exists already, in the people and suspended containers.
		existing := map[string]bool{}
		for _, base := range []string{cfg.Users.BaseDN, cfg.Users.SuspendedBaseDN} {
			if base == "" {
				continue
			}
			es, err := c.SearchAttrs(base, "(uid=*)", []string{"uid"}, 0)
			if err != nil && !ldapclient.IsCode(err, ldapclient.CodeNoSuchObject) {
				return err
			}
			for _, e := range es {
				existing[strings.ToLower(first(e.Values("uid")))] = true
			}
		}
		seen := map[string]int{}
		for i := range rows {
			row, p := &rows[i], people[i]
			username, _, verr := s.validatePerson(cfg, p)
			switch {
			case verr != nil:
				row.Status, row.Message = "error", importMessage(verr)
			case existing[username]:
				row.Status, row.Message = "error", "The username “"+username+"” is already taken."
			case seen[username] > 0:
				row.Status, row.Message = "error", "The username “"+username+"” is also on row "+itoa(seen[username])+" of this file."
			}
			row.Username = strings.ToLower(strings.TrimSpace(p.Username)) // shown even when the row is rejected
			if row.Status == "ready" {
				seen[username] = row.Row
			}
			if row.Status != "ready" || in.DryRun {
				if row.Status == "error" {
					failed++
				}
				continue
			}
			made, err := s.createPerson(c, cfg, p)
			if err != nil {
				row.Status, row.Message = "error", importMessage(err)
				failed++
				continue
			}
			row.Status, row.Temp = "created", made.TempPassword
			created++
			audit(sess, "user.create", made.DN, "via", "import")
		}
		return nil
	}) {
		return
	}
	if !in.DryRun {
		audit(sess, "user.import", "csv", "created", itoa(created), "failed", itoa(failed))
	} else {
		for _, row := range rows {
			if row.Status == "ready" {
				created++
			}
		}
	}
	writeJSON(w, map[string]any{"rows": rows, "ready": map[bool]int{true: created, false: 0}[in.DryRun], "created": map[bool]int{true: 0, false: created}[in.DryRun],
		"failed": failed, "total": len(rows), "dryRun": in.DryRun})
}

// importMessage turns an error into text for one row.
func importMessage(err error) string {
	var ve validationError
	if errors.As(err, &ve) {
		return ve.Error()
	}
	_, msg := friendlyError(err)
	return msg
}

// ---- bulk actions ----

type bulkResult struct {
	DN      string `json:"dn"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func (s *Server) bulkUsers(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	var in struct {
		Action    string   `json:"action"` // suspend, restore, delete, addGroup, removeGroup
		DNs       []string `json:"dns"`
		Group     string   `json:"group"`
		Confirmed bool     `json:"confirmed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if len(in.DNs) == 0 || len(in.DNs) > maxBulk {
		fail(w, http.StatusBadRequest, "Choose between 1 and 200 people.")
		return
	}
	switch in.Action {
	case "suspend", "restore":
	case "delete":
		if !in.Confirmed {
			fail(w, http.StatusBadRequest, "Please confirm that you want to delete these accounts.")
			return
		}
	case "addGroup", "removeGroup":
		if _, err := s.groupDN(in.Group); err != nil {
			failLDAP(w, err)
			return
		}
		if eqFold(in.Group, s.c().Admin.GroupDN) && in.Action == "removeGroup" {
			for _, dn := range in.DNs {
				if eqFold(dn, sess.DN) {
					fail(w, http.StatusBadRequest, "You can't remove yourself from the administrators group.")
					return
				}
			}
		}
	default:
		fail(w, http.StatusBadRequest, "Unknown action.")
		return
	}
	results := make([]bulkResult, 0, len(in.DNs))
	done := 0
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		for _, raw := range in.DNs {
			res := bulkResult{DN: raw}
			dn, err := s.userDN(raw)
			if err == nil {
				switch in.Action {
				case "suspend", "restore":
					_, err = s.suspendPerson(c, sess, dn, in.Action == "suspend")
				case "delete":
					err = s.deletePerson(c, sess, dn)
				case "addGroup":
					err = s.addMember(c, in.Group, dn)
				case "removeGroup":
					err = s.removeMember(c, in.Group, dn)
					if err == nil && eqFold(in.Group, s.c().Admin.GroupDN) {
						s.sessions.RevokeFor(dn, "")
					}
				}
			}
			if err != nil {
				res.Message = importMessage(err)
			} else {
				res.OK = true
				done++
			}
			results = append(results, res)
		}
		return nil
	}) {
		return
	}
	audit(sess, "user.bulk", in.Action, "done", itoa(done), "failed", itoa(len(results)-done), "at", time.Now().UTC().Format(time.RFC3339))
	writeJSON(w, map[string]any{"results": results, "done": done, "failed": len(results) - done})
}
