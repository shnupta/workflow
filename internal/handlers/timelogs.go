package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerTimeLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks/{id}/time-logs", h.apiListTimeLogs)
	mux.HandleFunc("POST /api/tasks/{id}/time-logs", h.apiCreateTimeLog)
	mux.HandleFunc("DELETE /api/time-logs/{id}", h.apiDeleteTimeLog)
	mux.HandleFunc("POST /api/tasks/{id}/archive", h.apiArchiveTask)
}

func (h *Handler) apiListTimeLogs(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	logs, err := h.db.ListTimeLogs(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]*timeLogResponse, len(logs))
	for i, l := range logs {
		out[i] = toTimeLogResponse(l)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (h *Handler) apiCreateTimeLog(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		DurationMins int    `json:"duration_mins"`
		Note         string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.DurationMins <= 0 {
		http.Error(w, "duration_mins must be positive", http.StatusBadRequest)
		return
	}
	body.Note = strings.TrimSpace(body.Note)
	l, err := h.db.LogTime(taskID, body.DurationMins, body.Note)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toTimeLogResponse(l))
}

func (h *Handler) apiDeleteTimeLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	if err := h.db.DeleteTimeLog(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type timeLogResponse struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	LoggedAt     string `json:"logged_at"`
	DurationMins int    `json:"duration_mins"`
	Note         string `json:"note"`
	FormattedTime string `json:"formatted_time"`
}

func toTimeLogResponse(l *models.TimeLog) *timeLogResponse {
	return &timeLogResponse{
		ID:            l.ID,
		TaskID:        l.TaskID,
		LoggedAt:      l.LoggedAt.UTC().Format("2006-01-02T15:04:05Z"),
		DurationMins:  l.DurationMins,
		Note:          l.Note,
		FormattedTime: l.FormattedTime(),
	}
}

// apiArchiveTask handles POST /api/tasks/{id}/archive — archives a task (removes from board).
// Body: {"archived": true|false}
func (h *Handler) apiArchiveTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		Archived bool `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.db.ArchiveTask(taskID, body.Archived); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"archived": body.Archived})
}
