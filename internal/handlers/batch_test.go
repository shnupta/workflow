package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBatchCreateTasks_Basic(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines":     "Fix the bug\nWrite docs\nDeploy to prod",
		"work_type": "coding",
		"tier":      "backlog",
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["created"].(float64)) != 3 {
		t.Errorf("expected created=3, got %v", resp["created"])
	}
	if len(resp["ids"].([]any)) != 3 {
		t.Errorf("expected 3 ids, got %d", len(resp["ids"].([]any)))
	}
}

func TestBatchCreateTasks_StripsBlankLines(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines": "Task A\n\n\nTask B\n   \n",
		"tier":  "today",
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["created"].(float64)) != 2 {
		t.Errorf("expected 2 tasks (blank lines skipped), got %v", resp["created"])
	}
}

func TestBatchCreateTasks_StripsBullets(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines": "- Fix auth\n* Update schema\n• Deploy\n1. Write tests",
		"tier":  "backlog",
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Verify titles are stripped of bullets
	tasks, _ := h.db.ListTasks(false, h.cfg())
	for _, task := range tasks {
		if strings.HasPrefix(task.Title, "-") || strings.HasPrefix(task.Title, "*") ||
			strings.HasPrefix(task.Title, "•") || strings.HasPrefix(task.Title, "1.") {
			t.Errorf("bullet not stripped from title: %q", task.Title)
		}
	}
}

func TestBatchCreateTasks_EmptyLines_CreatesZero(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines": "   \n\n  \n",
		"tier":  "backlog",
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["created"].(float64)) != 0 {
		t.Errorf("expected 0 tasks, got %v", resp["created"])
	}
}

func TestBatchCreateTasks_DefaultWorkType(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines": "Task without work type",
		"tier":  "backlog",
		// work_type omitted intentionally
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	tasks, _ := h.db.ListTasks(false, h.cfg())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].WorkType != "other" {
		t.Errorf("expected work_type 'other', got %q", tasks[0].WorkType)
	}
}

func TestBatchCreateTasks_InvalidJSON_Returns400(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", strings.NewReader("{not valid json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBatchCreateTasks_SingleLine(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"lines":     "One task only",
		"work_type": "pr_review",
		"tier":      "today",
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.batchCreateTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["created"].(float64)) != 1 {
		t.Errorf("expected 1, got %v", resp["created"])
	}
	tasks, _ := h.db.ListTasks(false, h.cfg())
	if len(tasks) != 1 || tasks[0].WorkType != "pr_review" || tasks[0].Tier != "today" {
		t.Errorf("unexpected task: %+v", tasks)
	}
}
