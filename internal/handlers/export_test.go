package handlers

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

// openTestHandler creates a Handler backed by a fresh in-memory SQLite DB.
func openTestHandler(t *testing.T) (*Handler, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "workflow-handler-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("open db: %v", err)
	}
	watcher, err := config.NewWatcher(filepath.Join(dir, "workflow.json"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("open config watcher: %v", err)
	}

	// Use a minimal glob that matches no files — templates aren't needed for
	// the export endpoint which writes plain CSV, not HTML.
	// We pass a non-existent glob; New() will error on ParseGlob. Work around
	// by writing a tiny stub template file so New() can initialise cleanly.
	stubDir := filepath.Join(dir, "templates")
	if err := os.Mkdir(stubDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	stub := filepath.Join(stubDir, "stub.html")
	if err := os.WriteFile(stub, []byte(`{{define "stub.html"}}{{end}}`), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	h, err := New(d, watcher, stub)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("New handler: %v", err)
	}

	return h, func() {
		os.RemoveAll(dir)
	}
}

func TestCSVExport_StatusOK(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/export/tasks.csv", nil)
	w := httptest.NewRecorder()
	h.exportTasksCSV(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestCSVExport_ContentTypeAndDisposition(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/export/tasks.csv", nil)
	w := httptest.NewRecorder()
	h.exportTasksCSV(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("expected Content-Type text/csv, got %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "tasks.csv") {
		t.Errorf("expected Content-Disposition to contain tasks.csv, got %q", cd)
	}
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected Content-Disposition to contain attachment, got %q", cd)
	}
}

func TestCSVExport_HasHeaderRow(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/export/tasks.csv", nil)
	w := httptest.NewRecorder()
	h.exportTasksCSV(w, req)

	r := csv.NewReader(w.Body)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected at least a header row, got nothing")
	}
	header := records[0]
	required := []string{"id", "title", "work_type", "status", "tier", "due_date",
		"recurrence", "timer_seconds", "time_tracked", "blocked_by", "created_at", "completed_at"}
	headerSet := make(map[string]bool, len(header))
	for _, col := range header {
		headerSet[col] = true
	}
	for _, col := range required {
		if !headerSet[col] {
			t.Errorf("header missing expected column %q", col)
		}
	}
}

func TestCSVExport_IncludesAllTasks(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	// Insert tasks across tiers, including a done one.
	tasks := []*models.Task{
		{Title: "T1", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"},
		{Title: "T2", WorkType: "meeting", Tier: "this_week", Direction: "blocked_on_me"},
		{Title: "T3", WorkType: "docs", Tier: "backlog", Direction: "blocked_on_me"},
	}
	for _, task := range tasks {
		if err := h.db.CreateTask(task); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}
	if _, err := h.db.MarkDone(tasks[0].ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/export/tasks.csv", nil)
	w := httptest.NewRecorder()
	h.exportTasksCSV(w, req)

	r := csv.NewReader(w.Body)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	// records[0] = header; records[1..] = data rows.
	dataRows := records[1:]
	// 3 tasks seeded, plus however many the template seed added (none — templates
	// are not tasks). Note: CloneTaskForRecurrence is not triggered here since
	// none of these tasks have recurrence set.
	if len(dataRows) != 3 {
		t.Errorf("expected 3 data rows, got %d", len(dataRows))
	}
}

func TestCSVExport_DoneStatusField(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "Done task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.db.MarkDone(task.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/export/tasks.csv", nil)
	w := httptest.NewRecorder()
	h.exportTasksCSV(w, req)

	r := csv.NewReader(w.Body)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(records))
	}

	// Find "status" column index from header.
	header := records[0]
	statusIdx := -1
	for i, col := range header {
		if col == "status" {
			statusIdx = i
			break
		}
	}
	if statusIdx < 0 {
		t.Fatal("status column not found in header")
	}
	if records[1][statusIdx] != "done" {
		t.Errorf("expected status=done, got %q", records[1][statusIdx])
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, ""},
		{-5, ""},
		{30, "<1m"},
		{59, "<1m"},
		{60, "1m"},
		{90, "1m"},
		{3600, "1h"},
		{3660, "1h 1m"},
		{5400, "1h 30m"},
		{7322, "2h 2m"},
	}
	for _, c := range cases {
		got := formatDuration(c.secs)
		if got != c.want {
			t.Errorf("formatDuration(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}

func TestTaskStatus(t *testing.T) {
	active := &models.Task{Tier: "today", Done: false}
	if taskStatus(active) != "today" {
		t.Errorf("expected today, got %q", taskStatus(active))
	}
	done := &models.Task{Tier: "today", Done: true}
	if taskStatus(done) != "done" {
		t.Errorf("expected done, got %q", taskStatus(done))
	}
}
