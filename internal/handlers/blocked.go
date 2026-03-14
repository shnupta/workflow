package handlers

import (
	"net/http"
)

// blockedPage renders /blocked — a dashboard of all tasks currently blocked
// by another task, with quick unblock actions.
func (h *Handler) blockedPage(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.ListBlockedTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render(w, "blocked.html", map[string]any{
		"Title":       "Blocked tasks",
		"Nav":         "blocked",
		"BlockedRows": rows,
	})
}

// apiUnblockTask clears the blocked_by relationship for a task (convenience
// endpoint used by the blocked page's inline unblock button).
// POST /api/tasks/{id}/unblock
func (h *Handler) apiUnblockTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := h.db.ClearBlockedBy(taskID); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}
