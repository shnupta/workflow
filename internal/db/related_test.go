package db

import (
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

func TestListRelatedTasks_EmptyWorkType_ReturnsNil(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	task := &models.Task{Title: "No type", WorkType: "", Tier: "today"}
	d.CreateTask(task)

	related, err := d.ListRelatedTasks(task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Errorf("expected 0 related for empty work_type, got %d", len(related))
	}
}

func TestListRelatedTasks_NoOtherTasks_ReturnsEmpty(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	task := &models.Task{Title: "Lone task", WorkType: "coding", Tier: "today"}
	d.CreateTask(task)

	related, err := d.ListRelatedTasks(task.ID, "coding")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Errorf("expected 0 related, got %d", len(related))
	}
}

func TestListRelatedTasks_ExcludesSelf(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	task := &models.Task{Title: "Task A", WorkType: "pr_review", Tier: "today"}
	d.CreateTask(task)

	related, err := d.ListRelatedTasks(task.ID, "pr_review")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range related {
		if r.ID == task.ID {
			t.Error("ListRelatedTasks should not include the task itself")
		}
	}
}

func TestListRelatedTasks_ReturnsMatchingWorkType(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	t1 := &models.Task{Title: "PR #1", WorkType: "pr_review", Tier: "today"}
	t2 := &models.Task{Title: "PR #2", WorkType: "pr_review", Tier: "this_week"}
	t3 := &models.Task{Title: "Deploy", WorkType: "deployment", Tier: "today"}
	d.CreateTask(t1)
	d.CreateTask(t2)
	d.CreateTask(t3)

	related, err := d.ListRelatedTasks(t1.ID, "pr_review")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 1 {
		t.Fatalf("expected 1 related pr_review task, got %d", len(related))
	}
	if related[0].ID != t2.ID {
		t.Errorf("expected t2, got %s", related[0].Title)
	}
}

func TestListRelatedTasks_ExcludesDone(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	t1 := &models.Task{Title: "Active PR", WorkType: "pr_review", Tier: "today"}
	t2 := &models.Task{Title: "Done PR", WorkType: "pr_review", Tier: "today"}
	d.CreateTask(t1)
	d.CreateTask(t2)
	if _, err := d.MarkDone(t2.ID); err != nil {
		t.Fatal(err)
	}

	related, err := d.ListRelatedTasks(t1.ID, "pr_review")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Errorf("expected 0 related (done task excluded), got %d", len(related))
	}
}

func TestListRelatedTasks_LimitFive(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	anchor := &models.Task{Title: "Anchor", WorkType: "coding", Tier: "today"}
	d.CreateTask(anchor)

	// Create 7 related tasks
	for i := 0; i < 7; i++ {
		task := &models.Task{Title: "Related", WorkType: "coding", Tier: "backlog"}
		d.CreateTask(task)
	}

	related, err := d.ListRelatedTasks(anchor.ID, "coding")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) > 5 {
		t.Errorf("expected at most 5 related tasks, got %d", len(related))
	}
}

func TestListRelatedTasks_TodayBeforeBacklog(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	anchor := &models.Task{Title: "Anchor", WorkType: "deployment", Tier: "today"}
	d.CreateTask(anchor)

	backlog := &models.Task{Title: "Backlog deploy", WorkType: "deployment", Tier: "backlog"}
	today := &models.Task{Title: "Today deploy", WorkType: "deployment", Tier: "today"}
	d.CreateTask(backlog)
	d.CreateTask(today)

	related, err := d.ListRelatedTasks(anchor.ID, "deployment")
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 2 {
		t.Fatalf("expected 2 related tasks, got %d", len(related))
	}
	// today should come before backlog
	if related[0].Tier != "today" {
		t.Errorf("expected today-tier task first, got tier=%s", related[0].Tier)
	}
}
