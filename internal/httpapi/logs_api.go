package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"land/internal/store"
)

func (s *Server) handleListLogs() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseLogFilter(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_filter", err.Error())
			return
		}
		entries, total, err := s.Store.QueryLogs(r.Context(), filter)
		if err != nil {
			s.fail(w, "query logs", err)
			return
		}
		items := make([]map[string]any, len(entries))
		for i, e := range entries {
			items[i] = map[string]any{
				"id":       e.ID,
				"ts":       e.TS.Unix(),
				"ip":       e.IP,
				"host":     e.Host,
				"action":   e.Action,
				"share_id": e.ShareID,
				"path":     e.Path,
				"result":   e.Result,
				"detail":   e.Detail,
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"entries":   items,
			"total":     total,
			"page":      filter.Page,
			"page_size": filter.PageSize,
		})
	})
}

// handleExportLogs streams the filtered log as CSV — the same query the viewer
// uses, so "what I see is what I export".
func (s *Server) handleExportLogs() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseLogFilter(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_filter", err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="land-logs.csv"`)
		// UTF-8 BOM so Excel on a Chinese locale opens it readably.
		w.Write([]byte{0xEF, 0xBB, 0xBF})

		cw := csv.NewWriter(w)
		defer cw.Flush()
		_ = cw.Write([]string{"时间", "IP", "主机名", "操作", "共享", "路径", "结果", "详情"})

		err = s.Store.StreamLogs(r.Context(), filter, func(e store.LogEntry) error {
			return cw.Write([]string{
				e.TS.Format("2006-01-02 15:04:05"),
				e.IP,
				e.Host,
				e.Action,
				strconv.FormatInt(e.ShareID, 10),
				e.Path,
				e.Result,
				e.Detail,
			})
		})
		if err != nil {
			s.logger().Error("导出日志失败", "error", err)
		}
	})
}

func parseLogFilter(r *http.Request) (store.LogFilter, error) {
	q := r.URL.Query()
	f := store.LogFilter{
		IP:       strings.TrimSpace(q.Get("ip")),
		Action:   strings.TrimSpace(q.Get("action")),
		Page:     atoiDefault(q.Get("page"), 1),
		PageSize: atoiDefault(q.Get("page_size"), 100),
	}
	if v := q.Get("from"); v != "" {
		ts, err := parseTimeParam(v)
		if err != nil {
			return f, err
		}
		f.From = ts
	}
	if v := q.Get("to"); v != "" {
		ts, err := parseTimeParam(v)
		if err != nil {
			return f, err
		}
		f.To = ts
	}
	return f, nil
}

func parseTimeParam(v string) (time.Time, error) {
	if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(unix, 0), nil
	}
	return time.Parse("2006-01-02", v)
}
