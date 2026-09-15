package admin

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func (a *API) queryAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	username := q.Get("username")
	action := q.Get("action")
	success := q.Get("success")
	from := q.Get("from")
	to := q.Get("to")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))

	sql := `SELECT event_time, username, client_ip, session_id, action, path, bytes, success, source, detail
		FROM audit_events WHERE event_time BETWEEN $1 AND $2`
	args := []any{parseTimeOr(from, time.Now().Add(-30*24*time.Hour)), parseTimeOr(to, time.Now())}
	i := 3
	if username != "" {
		sql += " AND username=$" + strconv.Itoa(i)
		args = append(args, username)
		i++
	}
	if action != "" {
		sql += " AND action=$" + strconv.Itoa(i)
		args = append(args, action)
		i++
	}
	if success == "true" || success == "false" {
		sql += " AND success=$" + strconv.Itoa(i)
		args = append(args, success == "true")
		i++
	}
	sql += " ORDER BY event_time DESC LIMIT $" + strconv.Itoa(i) + " OFFSET $" + strconv.Itoa(i+1)
	args = append(args, limit, offset)

	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "application/json")
	out := []map[string]any{}
	for rows.Next() {
		var et time.Time
		var uname string
		var cip *string
		var sid *string
		var act string
		var p string
		var b int64
		var ok bool
		var src *string
		var detail []byte
		if err := rows.Scan(&et, &uname, &cip, &sid, &act, &p, &b, &ok, &src, &detail); err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		rec := map[string]any{
			"event_time": et, "username": uname, "client_ip": cip, "session_id": sid,
			"action": act, "path": p, "bytes": b, "success": ok, "source": src,
			"detail": json.RawMessage(detail),
		}
		out = append(out, rec)
	}
	json.NewEncoder(w).Encode(out)
}

func (a *API) exportAuditCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from := parseTimeOr(q.Get("from"), time.Now().Add(-30*24*time.Hour))
	to := parseTimeOr(q.Get("to"), time.Now())
	rows, err := a.Pool.Query(r.Context(),
		`SELECT event_time, username, client_ip, action, path, bytes, success FROM audit_events
		 WHERE event_time BETWEEN $1 AND $2 ORDER BY event_time`, from, to)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{"event_time", "username", "client_ip", "action", "path", "bytes", "success"})
	for rows.Next() {
		var et time.Time
		var u string
		var cip *string
		var act string
		var p string
		var b int64
		var ok bool
		if err := rows.Scan(&et, &u, &cip, &act, &p, &b, &ok); err != nil {
			continue
		}
		cw.Write([]string{et.UTC().Format(time.RFC3339), u, deref(cip), act, p, strconv.FormatInt(b, 10), strconv.FormatBool(ok)})
	}
}

func parseTimeOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return def
	}
	return t
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
