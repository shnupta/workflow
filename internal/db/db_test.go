package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/models"
)

// openTestDB opens a fresh in-file SQLite DB in a temp directory.
// The caller must call cleanup() when done.
func openTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "workflow-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	path := filepath.Join(dir, "test.db")
	db, err := Open(path)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("open db: %v", err)
	}
	return db, func() {
		db.conn.Close()
		os.RemoveAll(dir)
	}
}

func newTask(title, tier string) *models.Task {
	return &models.Task{
		Title:     title,
		WorkType:  "coding",
		Tier:      tier,
		Direction: "blocked_on_me",
	}
}

var testCfg = &config.Default

// ── CreateTask / GetTask ──────────────────────────────────────────────────────

func TestCreateTask_AssignsID(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("My task", "today")
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == "" {
		t.Error("expected ID to be assigned after CreateTask")
	}
}

func TestCreateTask_CanBeRetrieved(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Retrieve me", "today")
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != "Retrieve me" {
		t.Errorf("expected title 'Retrieve me', got %q", got.Title)
	}
	if got.Tier != "today" {
		t.Errorf("expected tier 'today', got %q", got.Tier)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	_, err := db.GetTask("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent task, got nil")
	}
}

// ── Position ordering ─────────────────────────────────────────────────────────

func TestCreateTask_PositionIncrementsWithinTier(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	a := newTask("First", "today")
	b := newTask("Second", "today")
	c := newTask("Different tier", "backlog")

	db.CreateTask(a)
	db.CreateTask(b)
	db.CreateTask(c)

	if a.Position >= b.Position {
		t.Errorf("expected a.Position(%d) < b.Position(%d)", a.Position, b.Position)
	}
	if c.Position != 0 {
		t.Errorf("first task in new tier should have position 0, got %d", c.Position)
	}
}

// ── MarkDone ──────────────────────────────────────────────────────────────────

func TestMarkDone(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Done task", "today")
	db.CreateTask(task)

	if err := db.MarkDone(task.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	got, _ := db.GetTask(task.ID)
	if !got.Done {
		t.Error("expected task to be done after MarkDone")
	}
	if got.DoneAt == nil {
		t.Error("expected DoneAt to be set after MarkDone")
	}
}

// ── DeleteTask ────────────────────────────────────────────────────────────────

func TestDeleteTask(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Delete me", "today")
	db.CreateTask(task)

	if err := db.DeleteTask(task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	_, err := db.GetTask(task.ID)
	if err == nil {
		t.Error("expected error fetching deleted task, got nil")
	}
}

// ── ListTasks ─────────────────────────────────────────────────────────────────

func TestListTasks_ExcludesDoneByDefault(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	active := newTask("Active", "today")
	done := newTask("Done", "today")
	db.CreateTask(active)
	db.CreateTask(done)
	db.MarkDone(done.ID)

	tasks, err := db.ListTasks(false, testCfg)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, t2 := range tasks {
		if t2.ID == done.ID {
			t.Error("ListTasks(includeDone=false) should not return done tasks")
		}
	}
}

func TestListTasks_IncludesDoneWhenRequested(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Done", "today")
	db.CreateTask(task)
	db.MarkDone(task.ID)

	tasks, err := db.ListTasks(true, testCfg)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	found := false
	for _, t2 := range tasks {
		if t2.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Error("ListTasks(includeDone=true) should include done tasks")
	}
}

// ── UpdateBrief ───────────────────────────────────────────────────────────────

func TestUpdateBrief(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("PR thing", "today")
	db.CreateTask(task)

	if err := db.UpdateBrief(task.ID, "# Summary\nLooks good.", "done"); err != nil {
		t.Fatalf("UpdateBrief: %v", err)
	}

	got, _ := db.GetTask(task.ID)
	if got.Brief != "# Summary\nLooks good." {
		t.Errorf("unexpected brief: %q", got.Brief)
	}
	if got.BriefStatus != "done" {
		t.Errorf("unexpected brief_status: %q", got.BriefStatus)
	}
}

// ── GetTaskByPRURL ────────────────────────────────────────────────────────────

func TestGetTaskByPRURL_Found(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("PR review", "today")
	task.PRURL = "https://github.com/org/repo/pull/42"
	db.CreateTask(task)

	got, err := db.GetTaskByPRURL("https://github.com/org/repo/pull/42")
	if err != nil {
		t.Fatalf("GetTaskByPRURL: %v", err)
	}
	if got == nil || got.ID != task.ID {
		t.Error("expected to find task by PR URL")
	}
}

func TestGetTaskByPRURL_NotFound(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	got, err := db.GetTaskByPRURL("https://github.com/org/repo/pull/999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for unknown PR URL")
	}
}

func TestGetTaskByPRURL_IgnoresDone(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Done PR", "today")
	task.PRURL = "https://github.com/org/repo/pull/7"
	db.CreateTask(task)
	db.MarkDone(task.ID)

	got, err := db.GetTaskByPRURL("https://github.com/org/repo/pull/7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil — done tasks should be excluded from PR URL lookup")
	}
}

// ── Timer ─────────────────────────────────────────────────────────────────────

func TestTimerToggle_StartStop(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Timer task", "today")
	db.CreateTask(task)

	// Start timer
	got, err := db.TimerToggle(task.ID)
	if err != nil {
		t.Fatalf("TimerToggle start: %v", err)
	}
	if got.TimerStarted == nil {
		t.Error("expected TimerStarted to be set after first toggle")
	}

	// Simulate 2 seconds passing (timer accumulates whole seconds)
	time.Sleep(2 * time.Second)

	// Stop timer
	got, err = db.TimerToggle(task.ID)
	if err != nil {
		t.Fatalf("TimerToggle stop: %v", err)
	}
	if got.TimerStarted != nil {
		t.Error("expected TimerStarted to be nil after second toggle")
	}
	if got.TimerTotal < 1 {
		t.Errorf("expected TimerTotal >= 1 after stopping timer, got %d", got.TimerTotal)
	}
}

func TestTimerReset(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Timer reset", "today")
	db.CreateTask(task)
	db.TimerToggle(task.ID) // start
	time.Sleep(2 * time.Second)
	db.TimerToggle(task.ID) // stop, accumulate

	if err := db.TimerReset(task.ID); err != nil {
		t.Fatalf("TimerReset: %v", err)
	}

	got, _ := db.GetTask(task.ID)
	if got.TimerTotal != 0 {
		t.Errorf("expected TimerTotal=0 after reset, got %d", got.TimerTotal)
	}
	if got.TimerStarted != nil {
		t.Error("expected TimerStarted=nil after reset")
	}
}

// ── Sessions ──────────────────────────────────────────────────────────────────

func newSession(taskID, name string) *models.Session {
	return &models.Session{
		TaskID: taskID,
		Name:   name,
	}
}

func TestCreateSession_CanBeRetrieved(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Task", "today")
	db.CreateTask(task)

	sess := newSession(task.ID, "Initial review")
	if err := db.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Error("expected ID assigned after CreateSession")
	}

	got, err := db.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Name != "Initial review" {
		t.Errorf("expected name 'Initial review', got %q", got.Name)
	}
}

func TestListSessions_FiltersByTask(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	t1 := newTask("Task 1", "today")
	t2 := newTask("Task 2", "today")
	db.CreateTask(t1)
	db.CreateTask(t2)

	s1 := newSession(t1.ID, "Session A")
	s2 := newSession(t2.ID, "Session B")
	db.CreateSession(s1)
	db.CreateSession(s2)

	sessions, err := db.ListSessions(t1.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session for task 1, got %d", len(sessions))
	}
	if sessions[0].ID != s1.ID {
		t.Error("wrong session returned")
	}
}

func TestPinSession(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Task", "today")
	db.CreateTask(task)

	sess := newSession(task.ID, "Pin me")
	db.CreateSession(sess)

	if err := db.PinSession(sess.ID, true); err != nil {
		t.Fatalf("PinSession: %v", err)
	}

	got, _ := db.GetSession(sess.ID)
	if !got.Pinned {
		t.Error("expected session to be pinned")
	}
}

func TestArchiveSession(t *testing.T) {
	db, cleanup := openTestDB(t)
	defer cleanup()

	task := newTask("Task", "today")
	db.CreateTask(task)

	sess := newSession(task.ID, "Archive me")
	db.CreateSession(sess)

	if err := db.ArchiveSession(sess.ID, true); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	got, _ := db.GetSession(sess.ID)
	if !got.Archived {
		t.Error("expected session to be archived")
	}
}
