package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func TestApiArchiveTask_ArchiveAndUnarchive(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()
	task := &models.Task{Title: "Toggle archive", WorkType: "Coding", Tier: "today", Direction: "on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Archive
	body, _ := json.Marshal(map[string]bool{"archived": true})
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/archive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rec := httptest.NewRecorder()
	h.apiArchiveTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["archived"] {
		t.Error("expected archived=true in response")
	}

	// Archived task should appear in ListArchivedTasks
	archived, err := h.db.ListArchivedTasks()
	if err != nil {
		t.Fatalf("ListArchivedTasks: %v", err)
	}
	found := false
	for _, a := range archived {
		if a.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Error("task not found in archived list after archiving")
	}

	// Unarchive
	body2, _ := json.Marshal(map[string]bool{"archived": false})
	req2 := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/archive", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetPathValue("id", task.ID)
	rec2 := httptest.NewRecorder()
	h.apiArchiveTask(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unarchive: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 map[string]bool
	json.NewDecoder(rec2.Body).Decode(&resp2)
	if resp2["archived"] {
		t.Error("expected archived=false in response after unarchive")
	}

	// Should no longer appear in archived list
	archived2, _ := h.db.ListArchivedTasks()
	for _, a := range archived2 {
		if a.ID == task.ID {
			t.Error("task still in archived list after unarchiving")
		}
	}
}

func TestListArchivedTasks_ExcludedFromActive(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	// Create a task then archive it — it should not appear in ListArchivedTasks when active
	task := &models.Task{Title: "Should be archived", WorkType: "Coding", Tier: "today", Direction: "on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.db.ArchiveTask(task.ID, true); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}

	// ListArchivedTasks should include it
	archived, err := h.db.ListArchivedTasks()
	if err != nil {
		t.Fatalf("ListArchivedTasks: %v", err)
	}
	found := false
	for _, a := range archived {
		if a.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Error("archived task not found in ListArchivedTasks")
	}

	// Unarchive and it should disappear from archived list
	if err := h.db.ArchiveTask(task.ID, false); err != nil {
		t.Fatalf("ArchiveTask false: %v", err)
	}
	archived2, _ := h.db.ListArchivedTasks()
	for _, a := range archived2 {
		if a.ID == task.ID {
			t.Error("task still in archived list after unarchiving")
		}
	}
}
