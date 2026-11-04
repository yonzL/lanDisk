package httpapi

import (
	"net/http"

	"land/internal/store"
)

// siteResponse is the public, unauthenticated view of the site appearance.
func (s *Server) handleSite() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		get := func(key string) string {
			v, _, err := s.Store.Setting(r.Context(), key)
			if err != nil {
				return ""
			}
			return v
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"title":  get(store.KeySiteTitle),
			"notice": get(store.KeySiteNotice),
		})
	}
}
