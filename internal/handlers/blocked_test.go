package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func newBlockedTask(title, tier string) *models.Task {
	return &models.Task{
		Title:     title,
		WorkType:  "coding",
		Tier:      tier,
		Direction: "on_me",
	}
}

func TestAPIUnblockTask_ClearsBlockedBy(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("Blocker task", "today")
	if err := h.db.CreateTask(blocker); err != nil {
		t.Fatal(err)
	}
	blocked := newBlockedTask("Blocked task", "today")
	if err := h.db.CreateTask(blocked); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetBlockedBy(blocked.ID, blocker.ID); err != nil {
		t.Fatal(err)
	}

	// Verify it's blocked before
	task, err := h.db.GetTask(blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.BlockedBy == "" {
		t.Fatal("expected task to be blocked before unblock")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+blocked.ID+"/unblock", nil)
	req.SetPathValue("id", blocked.ID)
	w := httptest.NewRecorder()
	h.apiUnblockTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result["ok"] {
		t.Errorf("expected ok=true in response")
	}

	// Verify cleared in DB
	task, err = h.db.GetTask(blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.BlockedBy != "" {
		t.Errorf("expected blocked_by to be empty, got %q", task.BlockedBy)
	}
}

func TestAPIUnblockTask_NonExistentTaskNoError(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/nope/unblock", nil)
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	h.apiUnblockTask(w, req)

	// ClearBlockedBy on a non-existent task should be a no-op, not an error
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-existent task, got %d", w.Code)
	}
}

func TestCountBlockedTasks_ZeroInitially(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	n, err := h.db.CountBlockedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 blocked tasks on empty DB, got %d", n)
	}
}

func TestCountBlockedTasks_IncrementsOnBlock(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("B1", "today")
	h.db.CreateTask(blocker)
	blocked := newBlockedTask("B2", "this_week")
	h.db.CreateTask(blocked)
	h.db.SetBlockedBy(blocked.ID, blocker.ID)

	n, err := h.db.CountBlockedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 blocked task, got %d", n)
	}
}

func TestCountBlockedTasks_NotCountsDone(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("B1", "today")
	h.db.CreateTask(blocker)
	blocked := newBlockedTask("B2", "today")
	h.db.CreateTask(blocked)
	h.db.SetBlockedBy(blocked.ID, blocker.ID)
	h.db.MarkDone(blocked.ID)

	n, err := h.db.CountBlockedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 blocked tasks (task is done), got %d", n)
	}
}

func TestListBlockedTasks_ReturnsBlockedWithBlockerTitle(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("Fix infra", "today")
	h.db.CreateTask(blocker)
	blocked := newBlockedTask("Deploy feature", "today")
	h.db.CreateTask(blocked)
	h.db.SetBlockedBy(blocked.ID, blocker.ID)

	rows, err := h.db.ListBlockedTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Task.Title != "Deploy feature" {
		t.Errorf("expected blocked task title 'Deploy feature', got %q", rows[0].Task.Title)
	}
	if rows[0].BlockerTitle != "Fix infra" {
		t.Errorf("expected blocker title 'Fix infra', got %q", rows[0].BlockerTitle)
	}
	if rows[0].BlockerID != blocker.ID {
		t.Errorf("expected blocker ID %q, got %q", blocker.ID, rows[0].BlockerID)
	}
}
