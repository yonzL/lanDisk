package httpapi

import (
	"net/http"
	"sort"
	"strings"

	"land/internal/store"
)

type settingsResponse struct {
	Values map[string]string `json:"values"`
	Keys   []string          `json:"keys"`
}

func (s *Server) handleGetSettings() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		values, err := s.Store.Settings(r.Context())
		if err != nil {
			s.fail(w, "read settings", err)
			return
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeJSON(w, http.StatusOK, settingsResponse{Values: values, Keys: keys})
	})
}

// handlePutSettings updates the settings it is given and leaves the rest alone,
// so a partial update from one admin tab cannot reset another's changes.
func (s *Server) handlePutSettings() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Values map[string]string `json:"values"`
		}
		if !decodeJSON(w, r, &payload) {
			return
		}
		var changed []string
		for key, value := range payload.Values {
			if !store.KnownSetting(key) {
				writeError(w, http.StatusBadRequest, "unknown_setting", "未知配置项："+key)
				return
			}
			value = strings.TrimSpace(value)
			if key == store.KeySiteNotice {
				// The header renders the notice as one scrolling line, so it is
				// normalised on write: no newlines, no control characters, capped
				// length. HTML is no longer rendered, so tags would show verbatim.
				value = normalizeNotice(value)
			}
			if err := s.Store.SetSetting(r.Context(), key, value); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_setting", err.Error())
				return
			}
			changed = append(changed, key)
		}
		s.RefreshRuntimeLimits(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"updated": changed})
	})
}

// --- IP rules -------------------------------------------------------------

type ipRuleItem struct {
	CIDR      string `json:"cidr"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	Hits      int64  `json:"hits"`
	LastHitAt int64  `json:"last_hit_at"`
}

func (s *Server) handleListIPRules() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		rules, err := s.Store.ListIPRules(r.Context())
		if err != nil {
			s.fail(w, "list ip rules", err)
			return
		}
		items := make([]ipRuleItem, len(rules))
		for i, rule := range rules {
			var last int64
			if !rule.LastHitAt.IsZero() {
				last = rule.LastHitAt.Unix()
			}
			items[i] = ipRuleItem{CIDR: rule.CIDR, Kind: rule.Kind, Note: rule.Note, Hits: rule.Hits, LastHitAt: last}
		}
		writeJSON(w, http.StatusOK, items)
	})
}

func (s *Server) handleSetIPRule() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			CIDR string `json:"cidr"`
			Kind string `json:"kind"`
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &item) {
			return
		}
		cidr, ok := normalizeCIDR(item.CIDR)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_cidr", "IP / CIDR 不合法")
			return
		}
		if item.Kind != "allow" && item.Kind != "deny" {
			writeError(w, http.StatusBadRequest, "bad_kind", "kind 只能是 allow 或 deny")
			return
		}
		if err := s.Store.UpsertIPRule(r.Context(), cidr, item.Kind, item.Note, nowFunc()); err != nil {
			s.fail(w, "write ip rule", err)
			return
		}
		s.RefreshRuntimeLimits(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"cidr": cidr, "kind": item.Kind})
	})
}

func (s *Server) handleDeleteIPRule() http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		cidr, ok := normalizeCIDR(r.URL.Query().Get("cidr"))
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_cidr", "IP / CIDR 不合法")
			return
		}
		if err := s.Store.DeleteIPRule(r.Context(), cidr); err != nil {
			s.fail(w, "delete ip rule", err)
			return
		}
		s.RefreshRuntimeLimits(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})
}

// maxNoticeRunes keeps the announcement readable while scrolling.
const maxNoticeRunes = 200

// normalizeNotice collapses an announcement to a single line: newlines become
// spaces, control characters are dropped, and the length is capped.
func normalizeNotice(value string) string {
	replaced := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(value)
	cleaned := strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, replaced)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	runes := []rune(cleaned)
	if len(runes) > maxNoticeRunes {
		return string(runes[:maxNoticeRunes])
	}
	return cleaned
}
