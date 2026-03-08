package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerReminderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks/{id}/reminders", h.apiListReminders)
	mux.HandleFunc("POST /api/tasks/{id}/reminders", h.apiCreateReminder)
	mux.HandleFunc("DELETE /api/reminders/{id}", h.apiDeleteReminder)
}

// apiListReminders returns all reminders for a task as a JSON array.
func (h *Handler) apiListReminders(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	reminders, err := h.db.ListRemindersForTask(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]*reminderResponse, len(reminders))
	for i, rem := range reminders {
		out[i] = toReminderResponse(rem)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// apiCreateReminder parses {"remind_at": "...", "note": "..."} and creates a
// reminder. remind_at must be parseable as RFC3339, a datetime-local value
// ("2006-01-02T15:04"), or a date ("2006-01-02").
func (h *Handler) apiCreateReminder(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		RemindAt string `json:"remind_at"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.RemindAt = strings.TrimSpace(body.RemindAt)
	if body.RemindAt == "" {
		http.Error(w, "remind_at is required", http.StatusBadRequest)
		return
	}
	remindAt, err := parseReminderInput(body.RemindAt)
	if err != nil {
		http.Error(w, "invalid remind_at: "+err.Error(), http.StatusBadRequest)
		return
	}

	rem, err := h.db.CreateReminder(taskID, remindAt, strings.TrimSpace(body.Note))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toReminderResponse(rem))
}

// apiDeleteReminder removes a reminder by ID. 204 on success; 400 on bad ID.
func (h *Handler) apiDeleteReminder(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid reminder id", http.StatusBadRequest)
		return
	}
	if err := h.db.DeleteReminder(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseReminderInput accepts RFC3339, datetime-local ("2006-01-02T15:04"),
// and date-only ("2006-01-02") formats. Returns UTC time.
func parseReminderInput(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as a date/time", s)
}

// reminderResponse is the JSON shape returned to the client.
type reminderResponse struct {
	ID               int64  `json:"id"`
	TaskID           string `json:"task_id"`
	RemindAt         string `json:"remind_at"`          // RFC3339
	RemindAtFormatted string `json:"remind_at_formatted"` // "Mar 9, 9:00am"
	Note             string `json:"note"`
	Sent             bool   `json:"sent"`
}

func toReminderResponse(rem *models.Reminder) *reminderResponse {
	return &reminderResponse{
		ID:                rem.ID,
		TaskID:            rem.TaskID,
		RemindAt:          rem.RemindAt.UTC().Format(time.RFC3339),
		RemindAtFormatted: rem.RemindAtFormatted(),
		Note:              rem.Note,
		Sent:              rem.Sent,
	}
}
