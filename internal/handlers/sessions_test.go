package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

// helper to create a task and a session attached to it.
func createTestSession(t *testing.T, h *Handler, taskTier string) (*models.Task, *models.Session) {
	t.Helper()
	task := &models.Task{Title: "task for session", WorkType: "coding", Tier: taskTier, Direction: "on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	sess := &models.Session{
		TaskID: task.ID,
		Name:   "initial name",
		Status: models.SessionStatusComplete,
		Mode:   models.SessionModeInteractive,
	}
	if err := h.db.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return task, sess
}

// ── createSession ─────────────────────────────────────────────────────────

func TestCreateSession_MissingTaskReturns404(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body := strings.NewReader(`{"prompt":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/nonexistent/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	rr := httptest.NewRecorder()
	h.createSession(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestCreateSession_EmptyPromptReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "t", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	body := strings.NewReader(`{"prompt":""}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.createSession(rr, req)

	// Should fail because prompt is empty
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 400 or 503 for empty prompt, got %d", rr.Code)
	}
}

// ── renameSession ─────────────────────────────────────────────────────────

func TestRenameSession_UpdatesName(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"name":"renamed session"}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/rename", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.renameSession(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, err := h.db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if updated.Name != "renamed session" {
		t.Errorf("expected name 'renamed session', got %q", updated.Name)
	}
}

func TestRenameSession_EmptyNameReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/rename", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.renameSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty name, got %d", rr.Code)
	}
}

func TestRenameSession_WhitespaceNameReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"name":"   "}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/rename", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.renameSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for whitespace-only name, got %d", rr.Code)
	}
}

// ── pinSession ────────────────────────────────────────────────────────────

func TestPinSession_PinsSession(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"pinned":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/pin", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.pinSession(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := h.db.GetSession(sess.ID)
	if !updated.Pinned {
		t.Error("expected session to be pinned")
	}
}

func TestPinSession_UnpinsSession(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	// Pin first
	h.db.PinSession(sess.ID, true)

	// Unpin
	body := strings.NewReader(`{"pinned":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/pin", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.pinSession(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}

	updated, _ := h.db.GetSession(sess.ID)
	if updated.Pinned {
		t.Error("expected session to be unpinned")
	}
}

// ── archiveSession ────────────────────────────────────────────────────────

func TestArchiveSession_Archives(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"archived":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/archive", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.archiveSession(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := h.db.GetSession(sess.ID)
	if !updated.Archived {
		t.Error("expected session to be archived")
	}
}

func TestArchiveSession_Unarchives(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")
	h.db.ArchiveSession(sess.ID, true)

	body := strings.NewReader(`{"archived":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/archive", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.archiveSession(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}

	updated, _ := h.db.GetSession(sess.ID)
	if updated.Archived {
		t.Error("expected session to be unarchived")
	}
}

// ── setSessionFeedback ────────────────────────────────────────────────────

func TestSetSessionFeedback_Up(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"feedback":"up"}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/feedback", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.setSessionFeedback(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := h.db.GetSession(sess.ID)
	if updated.Feedback != "up" {
		t.Errorf("expected feedback 'up', got %q", updated.Feedback)
	}
}

func TestSetSessionFeedback_Down(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"feedback":"down"}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/feedback", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.setSessionFeedback(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}

	updated, _ := h.db.GetSession(sess.ID)
	if updated.Feedback != "down" {
		t.Errorf("expected feedback 'down', got %q", updated.Feedback)
	}
}

func TestSetSessionFeedback_InvalidValueReturns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")

	body := strings.NewReader(`{"feedback":"meh"}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/feedback", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.setSessionFeedback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid feedback, got %d", rr.Code)
	}
}

func TestSetSessionFeedback_ClearFeedback(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	_, sess := createTestSession(t, h, "today")
	// Set then clear
	h.db.SetSessionFeedback(sess.ID, "up")

	body := strings.NewReader(`{"feedback":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/sessions/"+sess.ID+"/feedback", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.setSessionFeedback(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 when clearing, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := h.db.GetSession(sess.ID)
	if updated.Feedback != "" {
		t.Errorf("expected feedback cleared, got %q", updated.Feedback)
	}
}

// ── listSessions ──────────────────────────────────────────────────────────

func TestListSessions_ReturnsJSONArray(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task, _ := createTestSession(t, h, "today")

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/sessions", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.listSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 session, got %d", len(result))
	}
}

func TestListSessions_EmptyForNewTask(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "no sessions", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID+"/sessions", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.listSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var result []map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("expected 0 sessions for new task, got %d", len(result))
	}
}

// ── exportSessionMarkdown ─────────────────────────────────────────────────

func TestExportSessionMarkdown_ContainsTaskTitle(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task, sess := createTestSession(t, h, "today")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/sessions/"+sess.ID+"/export.md", nil)
	req.SetPathValue("id", task.ID)
	req.SetPathValue("sid", sess.ID)
	rr := httptest.NewRecorder()
	h.exportSessionMarkdown(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, task.Title) {
		t.Errorf("expected markdown to contain task title %q, got:\n%s", task.Title, body)
	}
	if !strings.Contains(body, sess.Name) {
		t.Errorf("expected markdown to contain session name %q", sess.Name)
	}
}

func TestExportSessionMarkdown_MismatchedTaskSessionReturns404(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task1, _ := createTestSession(t, h, "today")
	task2, sess2 := createTestSession(t, h, "today")
	_ = task2

	// Request task1's export with sess2's ID (different task)
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task1.ID+"/sessions/"+sess2.ID+"/export.md", nil)
	req.SetPathValue("id", task1.ID)
	req.SetPathValue("sid", sess2.ID)
	rr := httptest.NewRecorder()
	h.exportSessionMarkdown(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for mismatched task/session, got %d", rr.Code)
	}
}
