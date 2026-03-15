package db

import (
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func TestGetTaskStats_Empty(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalOpen != 0 {
		t.Errorf("expected 0 open, got %d", stats.TotalOpen)
	}
	if stats.TotalDone != 0 {
		t.Errorf("expected 0 done, got %d", stats.TotalDone)
	}
}

func TestGetTaskStats_TotalCounts(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	// Create 3 open tasks and mark 1 done
	for _, title := range []string{"A", "B", "C"} {
		task := &models.Task{Title: title, WorkType: "coding", Tier: "today"}
		if err := d.CreateTask(task); err != nil {
			t.Fatal(err)
		}
		if title == "A" {
			if _, err := d.MarkDone(task.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalOpen != 2 {
		t.Errorf("expected 2 open, got %d", stats.TotalOpen)
	}
	if stats.TotalDone != 1 {
		t.Errorf("expected 1 done, got %d", stats.TotalDone)
	}
}

func TestGetTaskStats_ByWorkType(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	for _, wt := range []string{"coding", "coding", "pr_review"} {
		task := &models.Task{Title: wt + " task", WorkType: wt, Tier: "today"}
		if err := d.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]int{}
	for _, r := range stats.ByWorkType {
		found[r.WorkType] = r.Open
	}
	if found["coding"] != 2 {
		t.Errorf("expected coding=2, got %d", found["coding"])
	}
	if found["pr_review"] != 1 {
		t.Errorf("expected pr_review=1, got %d", found["pr_review"])
	}
}

func TestGetTaskStats_ByTier(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	tasks := []*models.Task{
		{Title: "T1", WorkType: "coding", Tier: "today"},
		{Title: "T2", WorkType: "coding", Tier: "today"},
		{Title: "T3", WorkType: "coding", Tier: "this_week"},
		{Title: "T4", WorkType: "coding", Tier: "backlog"},
	}
	for _, task := range tasks {
		if err := d.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]int{}
	for _, r := range stats.ByTier {
		found[r.Tier] = r.Count
	}
	if found["today"] != 2 {
		t.Errorf("expected today=2, got %d", found["today"])
	}
	if found["this_week"] != 1 {
		t.Errorf("expected this_week=1, got %d", found["this_week"])
	}
	if found["backlog"] != 1 {
		t.Errorf("expected backlog=1, got %d", found["backlog"])
	}
}

func TestGetTaskStats_ByPriority(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	tasks := []*models.Task{
		{Title: "P1 task", WorkType: "coding", Tier: "today", Priority: "p1"},
		{Title: "P2 task", WorkType: "coding", Tier: "today", Priority: "p2"},
		{Title: "No prio", WorkType: "coding", Tier: "backlog"},
	}
	for _, task := range tasks {
		if err := d.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]int{}
	for _, r := range stats.ByPriority {
		found[r.Priority] = r.Count
	}
	if found["p1"] != 1 {
		t.Errorf("expected p1=1, got %d", found["p1"])
	}
	if found["p2"] != 1 {
		t.Errorf("expected p2=1, got %d", found["p2"])
	}
	if found["none"] != 1 {
		t.Errorf("expected none=1, got %d", found["none"])
	}
}

func TestGetTaskStats_DoneExcludedFromOpen(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	t1 := &models.Task{Title: "Done one", WorkType: "deployment", Tier: "today"}
	d.CreateTask(t1)
if _, err := d.MarkDone(t1.ID); err != nil { t.Fatal(err) }

	t2 := &models.Task{Title: "Open one", WorkType: "deployment", Tier: "today"}
	d.CreateTask(t2)

	stats, err := d.GetTaskStats()
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]struct{ open, done int }{}
	for _, r := range stats.ByWorkType {
		found[r.WorkType] = struct{ open, done int }{r.Open, r.Done}
	}
	if found["deployment"].open != 1 {
		t.Errorf("expected deployment open=1, got %d", found["deployment"].open)
	}
	if found["deployment"].done != 1 {
		t.Errorf("expected deployment done=1, got %d", found["deployment"].done)
	}
}
