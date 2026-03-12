package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

// TestAPIMarkDone verifies that POST /api/tasks/{id}/done marks the task done
// and returns JSON without redirecting.
func TestAPIMarkDone_MarksTaskDone(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{
		Title:     "In-progress task",
		WorkType:  "Coding",
		Tier:      "today",
		Direction: "on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/done", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiMarkDone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]bool
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify in DB
	updated, err := h.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !updated.Done {
		t.Error("expected task.Done to be true after apiMarkDone")
	}
	if updated.DoneAt == nil {
		t.Error("expected DoneAt to be set")
	}
}

// TestAPIMarkDone_RecurringTaskCloned verifies that a recurring task produces
// cloned=true in the response.
func TestAPIMarkDone_RecurringTaskCloned(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{
		Title:      "Daily standup",
		WorkType:   "Meeting",
		Tier:       "today",
		Direction:  "on_me",
		Recurrence: "daily",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/done", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiMarkDone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]bool
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result["cloned"] {
		t.Error("expected cloned=true for recurring task")
	}
}
