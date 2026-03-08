package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"

	"github.com/shnupta/workflow/internal/models"
)

func (h *Handler) registerExportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /export/tasks.csv", h.exportTasksCSV)
}

// exportTasksCSV handles GET /export/tasks.csv.
// Returns all tasks (every tier and status) as a CSV download sorted by
// created_at DESC.
func (h *Handler) exportTasksCSV(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.db.ListAllTasks()
	if err != nil {
		http.Error(w, "failed to load tasks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tasks.csv"`)

	cw := csv.NewWriter(w)

	// Header row.
	if err := cw.Write([]string{
		"id",
		"title",
		"work_type",
		"status",
		"tier",
		"due_date",
		"recurrence",
		"timer_seconds",
		"time_tracked",
		"blocked_by",
		"created_at",
		"completed_at",
	}); err != nil {
		// Headers already flushed; nothing useful we can do.
		return
	}

	for _, t := range tasks {
		if err := cw.Write(taskToCSVRow(t)); err != nil {
			return
		}
	}

	cw.Flush()
}

// taskToCSVRow converts a Task to a CSV row in the same column order as the
// header written by exportTasksCSV.
func taskToCSVRow(t *models.Task) []string {
	status := taskStatus(t)
	dueDate := ""
	if t.DueDate != nil {
		dueDate = t.DueDate.Format("2006-01-02")
	}
	completedAt := ""
	if t.DoneAt != nil {
		completedAt = t.DoneAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	secs := t.ElapsedSeconds()
	return []string{
		t.ID,
		t.Title,
		t.WorkType,
		status,
		t.Tier,
		dueDate,
		t.Recurrence,
		fmt.Sprintf("%d", secs),
		formatDuration(secs),
		t.BlockedBy,
		t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		completedAt,
	}
}

// taskStatus returns a human-readable status string for a task.
// Done tasks return "done" regardless of tier; active tasks return their tier.
func taskStatus(t *models.Task) string {
	if t.Done {
		return "done"
	}
	return t.Tier
}

// formatDuration converts a total-seconds int into "Xh Ym" / "Ym" / "" format.
// Returns "" for zero (no time tracked).
func formatDuration(secs int) string {
	if secs <= 0 {
		return ""
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	if h > 0 && m > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh", h)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	// Less than a minute but non-zero — show "<1m".
	return "<1m"
}
