package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func TestMoveTask_ChangesColumn(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Move me",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"tier": "backlog", "before_id": ""})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	w := httptest.NewRecorder()

	h.moveTask(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", w.Code, w.Body.String())
	}

	// Confirm tier was persisted.
	got, err := h.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask after move: %v", err)
	}
	if got.Tier != "backlog" {
		t.Errorf("expected tier=backlog after move, got %q", got.Tier)
	}
}

func TestMoveTask_InvalidBody(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/tasks/nonexistent/move", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	h.moveTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}
}

func TestMoveTask_AppendToColumn(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	// Three tasks in today; move one to this_week at the end.
	t1 := &models.Task{Title: "A", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	t2 := &models.Task{Title: "B", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	for _, task := range []*models.Task{t1, t2} {
		if err := h.db.CreateTask(task); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}

	body, _ := json.Marshal(map[string]string{"tier": "this_week", "before_id": ""})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+t1.ID+"/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", t1.ID)
	w := httptest.NewRecorder()

	h.moveTask(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	got, err := h.db.GetTask(t1.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Tier != "this_week" {
		t.Errorf("expected tier=this_week, got %q", got.Tier)
	}
}
