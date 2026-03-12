package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shnupta/workflow/internal/models"
)

func createTestTaskForTimeLogs(t *testing.T, h *Handler) *models.Task {
	t.Helper()
	task := &models.Task{
		Title:     "Time log test task",
		WorkType:  "Coding",
		Tier:      "today",
		Direction: "on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

func TestLogTime_CreateAndList(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := createTestTaskForTimeLogs(t, h)

	body, _ := json.Marshal(map[string]interface{}{
		"duration_mins": 25,
		"note":          "Initial implementation",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/time-logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiCreateTimeLog(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var created timeLogResponse
	json.NewDecoder(rr.Body).Decode(&created)
	if created.DurationMins != 25 {
		t.Errorf("duration_mins: want 25, got %d", created.DurationMins)
	}
	if created.Note != "Initial implementation" {
		t.Errorf("note mismatch: %q", created.Note)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/time-logs", nil)
	req2.SetPathValue("id", task.ID)
	rr2 := httptest.NewRecorder()
	h.apiListTimeLogs(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rr2.Code)
	}
	var logs []timeLogResponse
	json.NewDecoder(rr2.Body).Decode(&logs)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
}

func TestLogTime_ZeroDurationRejected(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := createTestTaskForTimeLogs(t, h)

	body, _ := json.Marshal(map[string]interface{}{"duration_mins": 0})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/time-logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiCreateTimeLog(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for zero duration, got %d", rr.Code)
	}
}

func TestLogTime_DeleteEntry(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := createTestTaskForTimeLogs(t, h)

	l, err := h.db.LogTime(task.ID, 30, "testing")
	if err != nil {
		t.Fatalf("LogTime: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/time-logs/"+l.ID, nil)
	req.SetPathValue("id", l.ID)
	rr := httptest.NewRecorder()
	h.apiDeleteTimeLog(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rr.Code)
	}

	logs, _ := h.db.ListTimeLogs(task.ID)
	if len(logs) != 0 {
		t.Errorf("expected 0 logs after delete, got %d", len(logs))
	}
}

func TestLogTime_EmptyListForNewTask(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := createTestTaskForTimeLogs(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/time-logs", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiListTimeLogs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var logs []timeLogResponse
	json.NewDecoder(rr.Body).Decode(&logs)
	if len(logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(logs))
	}
}

func TestDB_LogTime_OrderedOldestFirst(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := createTestTaskForTimeLogs(t, h)

	l1, _ := h.db.LogTime(task.ID, 10, "first")
	time.Sleep(10 * time.Millisecond)
	l2, _ := h.db.LogTime(task.ID, 20, "second")

	logs, err := h.db.ListTimeLogs(task.ID)
	if err != nil {
		t.Fatalf("ListTimeLogs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2, got %d", len(logs))
	}
	if logs[0].ID != l1.ID {
		t.Errorf("expected first log to be oldest")
	}
	if logs[1].ID != l2.ID {
		t.Errorf("expected second log to be newest")
	}
}
