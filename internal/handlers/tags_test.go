package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func createTagTask(t *testing.T, h *Handler) *models.Task {
	t.Helper()
	task := &models.Task{Title: "task with tags", WorkType: "coding", Tier: "today", Direction: "on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task
}

// ── apiAddTag ──────────────────────────────────────────────────────────────

func TestAPIAddTag_AddsTagAndReturnsAll(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	body := strings.NewReader(`{"tag":"backend"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/tags", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiAddTag(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var tags []string
	json.NewDecoder(rr.Body).Decode(&tags)
	if len(tags) != 1 || tags[0] != "backend" {
		t.Errorf("expected [backend], got %v", tags)
	}
}

func TestAPIAddTag_NormalisesToLowercase(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	body := strings.NewReader(`{"tag":"URGENT"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/tags", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiAddTag(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var tags []string
	json.NewDecoder(rr.Body).Decode(&tags)
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("expected [urgent], got %v", tags)
	}
}

func TestAPIAddTag_EmptyTagReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	body := strings.NewReader(`{"tag":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/tags", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiAddTag(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty tag, got %d", rr.Code)
	}
}

func TestAPIAddTag_WhitespaceTagReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	body := strings.NewReader(`{"tag":"  "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/tags", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiAddTag(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for whitespace tag, got %d", rr.Code)
	}
}

func TestAPIAddTag_MultipleTags(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	for _, tag := range []string{"alpha", "beta", "gamma"} {
		body := strings.NewReader(`{"tag":"` + tag + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/tags", body)
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", task.ID)
		h.apiAddTag(httptest.NewRecorder(), req)
	}

	tags, err := h.db.ListTags(task.ID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(tags), tags)
	}
}

// ── apiRemoveTag ───────────────────────────────────────────────────────────

func TestAPIRemoveTag_RemovesTag(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)
	h.db.AddTag(task.ID, "removeme")

	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+task.ID+"/tags/removeme", nil)
	req.SetPathValue("id", task.ID)
	req.SetPathValue("tag", "removeme")
	rr := httptest.NewRecorder()
	h.apiRemoveTag(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	tags, _ := h.db.ListTags(task.ID)
	for _, tag := range tags {
		if tag == "removeme" {
			t.Error("tag 'removeme' should have been deleted")
		}
	}
}

func TestAPIRemoveTag_NonExistentTagIsIdempotent(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := createTagTask(t, h)

	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+task.ID+"/tags/ghost", nil)
	req.SetPathValue("id", task.ID)
	req.SetPathValue("tag", "ghost")
	rr := httptest.NewRecorder()
	h.apiRemoveTag(rr, req)

	// Should not error — removing a nonexistent tag is fine
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 for nonexistent tag, got %d", rr.Code)
	}
}

// ── apiListAllTags ─────────────────────────────────────────────────────────

func TestAPIListAllTags_ReturnsEmpty(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rr := httptest.NewRecorder()
	h.apiListAllTags(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var tags []string
	json.NewDecoder(rr.Body).Decode(&tags)
	if len(tags) != 0 {
		t.Errorf("expected empty, got %v", tags)
	}
}

func TestAPIListAllTags_ReturnsAllUniqueTags(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task1 := createTagTask(t, h)
	task2 := &models.Task{Title: "task2", WorkType: "review", Tier: "week", Direction: "on_me"}
	h.db.CreateTask(task2)

	h.db.AddTag(task1.ID, "frontend")
	h.db.AddTag(task1.ID, "urgent")
	h.db.AddTag(task2.ID, "frontend") // duplicate across tasks
	h.db.AddTag(task2.ID, "backend")

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rr := httptest.NewRecorder()
	h.apiListAllTags(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var tags []string
	json.NewDecoder(rr.Body).Decode(&tags)
	// Should have 3 unique tags: backend, frontend, urgent
	if len(tags) != 3 {
		t.Errorf("expected 3 unique tags, got %d: %v", len(tags), tags)
	}
}
