package handlers

import (
	"testing"
)

func TestListTasksBlockedBy_Empty(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := newBlockedTask("Standalone task", "today")
	if err := h.db.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	blocking, err := h.db.ListTasksBlockedBy(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 0 {
		t.Errorf("expected 0 tasks blocked by standalone task, got %d", len(blocking))
	}
}

func TestListTasksBlockedBy_ReturnsBlockedTasks(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("Infra task", "today")
	if err := h.db.CreateTask(blocker); err != nil {
		t.Fatal(err)
	}
	dep1 := newBlockedTask("Feature A", "this_week")
	dep2 := newBlockedTask("Feature B", "backlog")
	if err := h.db.CreateTask(dep1); err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateTask(dep2); err != nil {
		t.Fatal(err)
	}
	h.db.SetBlockedBy(dep1.ID, blocker.ID)
	h.db.SetBlockedBy(dep2.ID, blocker.ID)

	blocking, err := h.db.ListTasksBlockedBy(blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 2 {
		t.Fatalf("expected 2 tasks blocked by blocker, got %d", len(blocking))
	}
	titles := map[string]bool{}
	for _, bt := range blocking {
		titles[bt.Title] = true
	}
	if !titles["Feature A"] || !titles["Feature B"] {
		t.Errorf("unexpected titles: %v", titles)
	}
}

func TestListTasksBlockedBy_ExcludesDone(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blocker := newBlockedTask("Blocker", "today")
	h.db.CreateTask(blocker)
	dep := newBlockedTask("Done dep", "today")
	h.db.CreateTask(dep)
	h.db.SetBlockedBy(dep.ID, blocker.ID)
	h.db.MarkDone(dep.ID)

	blocking, err := h.db.ListTasksBlockedBy(blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 0 {
		t.Errorf("expected 0 (done task excluded), got %d", len(blocking))
	}
}

func TestListTasksBlockedBy_MultipleBlockers(t *testing.T) {
	// Each task can only have one blocker, but a blocker can block many tasks.
	// Verify that ListTasksBlockedBy returns all of them correctly.
	h, cleanup := openTestHandler(t)
	defer cleanup()

	gatekeeper := newBlockedTask("DB migration", "today")
	h.db.CreateTask(gatekeeper)

	deps := []string{"Service A upgrade", "Service B upgrade", "Service C upgrade"}
	for _, title := range deps {
		dep := newBlockedTask(title, "this_week")
		h.db.CreateTask(dep)
		h.db.SetBlockedBy(dep.ID, gatekeeper.ID)
	}

	blocking, err := h.db.ListTasksBlockedBy(gatekeeper.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 3 {
		t.Fatalf("expected 3 blocked tasks, got %d", len(blocking))
	}
}

func TestListTasksBlockedBy_OnlyAffectsSpecificBlocker(t *testing.T) {
	// Two separate blocker tasks — each should only return its own dependents.
	h, cleanup := openTestHandler(t)
	defer cleanup()

	blockerA := newBlockedTask("Blocker A", "today")
	blockerB := newBlockedTask("Blocker B", "today")
	h.db.CreateTask(blockerA)
	h.db.CreateTask(blockerB)

	depA := newBlockedTask("Dep of A", "this_week")
	depB := newBlockedTask("Dep of B", "backlog")
	h.db.CreateTask(depA)
	h.db.CreateTask(depB)
	h.db.SetBlockedBy(depA.ID, blockerA.ID)
	h.db.SetBlockedBy(depB.ID, blockerB.ID)

	blockingA, _ := h.db.ListTasksBlockedBy(blockerA.ID)
	blockingB, _ := h.db.ListTasksBlockedBy(blockerB.ID)

	if len(blockingA) != 1 || blockingA[0].Title != "Dep of A" {
		t.Errorf("blockerA should block only 'Dep of A', got %v", blockingA)
	}
	if len(blockingB) != 1 || blockingB[0].Title != "Dep of B" {
		t.Errorf("blockerB should block only 'Dep of B', got %v", blockingB)
	}
}
