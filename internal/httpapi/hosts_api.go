package httpapi

import (
	"encoding/csv"
	"net"
	"net/http"
	"strings"

	"land/internal/store"
)

type hostItem struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	Source   string `json:"source"`
	Note     string `json:"note"`
}

func (s *Server) handleListHosts() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		hosts, err := s.Store.ListHosts(r.Context())
		if err != nil {
			s.fail(w, "list hosts", err)
			return
		}
		items := make([]hostItem, len(hosts))
		for i, h := range hosts {
			items[i] = hostItem{IP: h.IP, Hostname: h.Hostname, Source: h.Source, Note: h.Note}
		}
		writeJSON(w, http.StatusOK, items)
	})
}

func (s *Server) handleSetHost() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var item hostItem
		if !decodeJSON(w, r, &item) {
			return
		}
		if net.ParseIP(strings.TrimSpace(item.IP)) == nil {
			writeError(w, http.StatusBadRequest, "bad_ip", "IP 地址不合法")
			return
		}
		if strings.TrimSpace(item.Hostname) == "" {
			writeError(w, http.StatusBadRequest, "bad_hostname", "主机名不能为空")
			return
		}
		if err := s.Store.SetHost(r.Context(), store.Host{
			IP:       strings.TrimSpace(item.IP),
			Hostname: strings.TrimSpace(item.Hostname),
			Source:   "manual",
			Note:     item.Note,
		}, nowFunc()); err != nil {
			s.fail(w, "write host", err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
}

func (s *Server) handleDeleteHost() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		ip := strings.TrimSpace(r.PathValue("ip"))
		if net.ParseIP(ip) == nil {
			writeError(w, http.StatusBadRequest, "bad_ip", "IP 地址不合法")
			return
		}
		if err := s.Store.DeleteHost(r.Context(), ip); err != nil {
			s.fail(w, "delete host", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})
}

// handleExportHosts writes the mapping table as CSV.
func (s *Server) handleExportHosts() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		hosts, err := s.Store.ListHosts(r.Context())
		if err != nil {
			s.fail(w, "list hosts", err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="land-hosts.csv"`)
		w.Write([]byte{0xEF, 0xBB, 0xBF})
		cw := csv.NewWriter(w)
		defer cw.Flush()
		_ = cw.Write([]string{"ip", "hostname", "source", "note"})
		for _, h := range hosts {
			_ = cw.Write([]string{h.IP, h.Hostname, h.Source, h.Note})
		}
	})
}

// handleImportHosts reads a CSV (ip,hostname[,note]) and upserts each row.
func (s *Server) handleImportHosts() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		reader := csv.NewReader(http.MaxBytesReader(w, r.Body, 1<<20))
		reader.FieldsPerRecord = -1
		records, err := reader.ReadAll()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_csv", "CSV 解析失败："+err.Error())
			return
		}
		imported := 0
		for i, row := range records {
			if i == 0 && len(row) > 0 && strings.EqualFold(strings.TrimSpace(row[0]), "ip") {
				continue // header
			}
			if len(row) < 2 {
				continue
			}
			ip := strings.TrimSpace(row[0])
			hostname := strings.TrimSpace(row[1])
			if net.ParseIP(ip) == nil || hostname == "" {
				continue
			}
			note := ""
			if len(row) > 2 {
				note = strings.TrimSpace(row[2])
			}
			if err := s.Store.SetHost(r.Context(), store.Host{IP: ip, Hostname: hostname, Source: "manual", Note: note}, nowFunc()); err != nil {
				s.fail(w, "import hosts", err)
				return
			}
			imported++
		}
		writeJSON(w, http.StatusOK, map[string]int{"imported": imported})
	})
}
