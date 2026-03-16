package db

import (
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func TestListSubSessions(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	// Create task
	task := &models.Task{Title: "parent task", WorkType: "coding", Tier: "today"}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// Create parent session
	parent := &models.Session{
		TaskID: task.ID, Name: "parent", Mode: models.SessionModeInteractive,
		Status: models.SessionStatusIdle,
	}
	if err := db.CreateSession(parent); err != nil {
		t.Fatal(err)
	}

	// List with no children — should return empty (not nil)
	subs, err := db.ListSubSessions(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Errorf("expected 0 subs, got %d", len(subs))
	}

	// Create a child session
	parentID := parent.ID
	child := &models.Session{
		TaskID: task.ID, ParentID: &parentID, Name: "child thread",
		Mode: models.SessionModeInteractive, Status: models.SessionStatusIdle,
	}
	if err := db.CreateSession(child); err != nil {
		t.Fatal(err)
	}

	subs, err = db.ListSubSessions(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 sub, got %d", len(subs))
	}
	if subs[0].Name != "child thread" {
		t.Errorf("expected child thread, got %s", subs[0].Name)
	}
	if subs[0].ParentID == nil || *subs[0].ParentID != parent.ID {
		t.Errorf("ParentID not set correctly")
	}
}

func TestListSubSessions_ArchivedExcluded(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := &models.Task{Title: "t", WorkType: "other", Tier: "today"}
	if err := db.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	parent := &models.Session{
		TaskID: task.ID, Name: "p", Mode: models.SessionModeInteractive,
		Status: models.SessionStatusIdle,
	}
	if err := db.CreateSession(parent); err != nil {
		t.Fatal(err)
	}
	parentID := parent.ID
	child := &models.Session{
		TaskID: task.ID, ParentID: &parentID, Name: "archived child",
		Mode: models.SessionModeInteractive, Status: models.SessionStatusIdle,
	}
	if err := db.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	// Archive the child
	if err := db.ArchiveSession(child.ID, true); err != nil {
		t.Fatal(err)
	}

	subs, err := db.ListSubSessions(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 {
		t.Errorf("archived child should be excluded, got %d subs", len(subs))
	}
}
