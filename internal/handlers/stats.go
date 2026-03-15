package handlers

import (
	"net/http"
)

func (h *Handler) statsPage(w http.ResponseWriter, r *http.Request) {
	stats, err := h.db.GetTaskStats()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "stats.html", map[string]interface{}{
		"Nav":   "stats",
		"Stats": stats,
	})
}
