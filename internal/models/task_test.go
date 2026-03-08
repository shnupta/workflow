package models

import (
	"testing"
	"time"
)

// ── IsOverdue ─────────────────────────────────────────────────────────────────

func TestIsOverdue_NoDueDate(t *testing.T) {
	task := &Task{}
	if task.IsOverdue() {
		t.Error("task with no due date should not be overdue")
	}
}

func TestIsOverdue_Done(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1)
	task := &Task{DueDate: &yesterday, Done: true}
	if task.IsOverdue() {
		t.Error("done task should not be overdue even if past due date")
	}
}

func TestIsOverdue_PastDate(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1)
	task := &Task{DueDate: &yesterday}
	if !task.IsOverdue() {
		t.Error("task with yesterday's due date should be overdue")
	}
}

func TestIsOverdue_FutureDate(t *testing.T) {
	tomorrow := time.Now().AddDate(0, 0, 1)
	task := &Task{DueDate: &tomorrow}
	if task.IsOverdue() {
		t.Error("task with future due date should not be overdue")
	}
}

func TestIsOverdue_Today(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	task := &Task{DueDate: &today}
	if task.IsOverdue() {
		t.Error("task due today should not be overdue")
	}
}

// ── IsDueToday ────────────────────────────────────────────────────────────────

func TestIsDueToday_NoDueDate(t *testing.T) {
	task := &Task{}
	if task.IsDueToday() {
		t.Error("task with no due date should not be due today")
	}
}

func TestIsDueToday_Done(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	task := &Task{DueDate: &today, Done: true}
	if task.IsDueToday() {
		t.Error("done task should not be due today")
	}
}

func TestIsDueToday_Today(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	task := &Task{DueDate: &today}
	if !task.IsDueToday() {
		t.Error("task due today should return IsDueToday=true")
	}
}

func TestIsDueToday_Yesterday(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1)
	task := &Task{DueDate: &yesterday}
	if task.IsDueToday() {
		t.Error("task due yesterday should not be due today")
	}
}

func TestIsDueToday_Tomorrow(t *testing.T) {
	tomorrow := time.Now().AddDate(0, 0, 1)
	task := &Task{DueDate: &tomorrow}
	if task.IsDueToday() {
		t.Error("task due tomorrow should not be due today")
	}
}

// ── ElapsedSeconds ────────────────────────────────────────────────────────────

func TestElapsedSeconds_NoTimer(t *testing.T) {
	task := &Task{}
	if task.ElapsedSeconds() != 0 {
		t.Errorf("expected 0, got %d", task.ElapsedSeconds())
	}
}

func TestElapsedSeconds_AccumulatedOnly(t *testing.T) {
	task := &Task{TimerTotal: 3600}
	if task.ElapsedSeconds() != 3600 {
		t.Errorf("expected 3600, got %d", task.ElapsedSeconds())
	}
}

func TestElapsedSeconds_TimerRunning(t *testing.T) {
	started := time.Now().Add(-30 * time.Second)
	task := &Task{TimerTotal: 60, TimerStarted: &started}
	elapsed := task.ElapsedSeconds()
	// Should be ~90s (60 accumulated + ~30 running). Allow ±2s for test execution time.
	if elapsed < 88 || elapsed > 95 {
		t.Errorf("expected ~90s, got %d", elapsed)
	}
}

// ── ElapsedLabel ─────────────────────────────────────────────────────────────

func TestElapsedLabel_Zero(t *testing.T) {
	task := &Task{}
	if label := task.ElapsedLabel(); label != "" {
		t.Errorf("expected empty label for zero elapsed, got %q", label)
	}
}

func TestElapsedLabel_LessThanMinute(t *testing.T) {
	task := &Task{TimerTotal: 45}
	if label := task.ElapsedLabel(); label != "< 1m" {
		t.Errorf("expected '< 1m', got %q", label)
	}
}

func TestElapsedLabel_Minutes(t *testing.T) {
	task := &Task{TimerTotal: 2*60 + 30} // 2m 30s
	if label := task.ElapsedLabel(); label != "2m" {
		t.Errorf("expected '2m', got %q", label)
	}
}

func TestElapsedLabel_Hours(t *testing.T) {
	task := &Task{TimerTotal: 3*3600 + 25*60} // 3h 25m
	if label := task.ElapsedLabel(); label != "3h 25m" {
		t.Errorf("expected '3h 25m', got %q", label)
	}
}

func TestElapsedLabel_ExactHour(t *testing.T) {
	task := &Task{TimerTotal: 3600}
	if label := task.ElapsedLabel(); label != "1h 0m" {
		t.Errorf("expected '1h 0m', got %q", label)
	}
}

// ── DirectionLabel ────────────────────────────────────────────────────────────

func TestDirectionLabel_OnMe(t *testing.T) {
	task := &Task{Direction: "blocked_on_me"}
	if task.DirectionLabel() != "On me" {
		t.Errorf("expected 'On me', got %q", task.DirectionLabel())
	}
}

func TestDirectionLabel_OnThem(t *testing.T) {
	task := &Task{Direction: "blocked_on_them"}
	if task.DirectionLabel() != "On them" {
		t.Errorf("expected 'On them', got %q", task.DirectionLabel())
	}
}

func TestDirectionLabel_Default(t *testing.T) {
	task := &Task{Direction: "unknown"}
	if task.DirectionLabel() != "On me" {
		t.Errorf("expected default 'On me', got %q", task.DirectionLabel())
	}
}

// ── IsBlocked ─────────────────────────────────────────────────────────────────

func TestIsBlocked_Empty(t *testing.T) {
	task := &Task{}
	if task.IsBlocked() {
		t.Error("task with no BlockedBy should not be blocked")
	}
}

func TestIsBlocked_WithValue(t *testing.T) {
	task := &Task{BlockedBy: "some-task-id"}
	if !task.IsBlocked() {
		t.Error("task with BlockedBy set should be blocked")
	}
}

// ── IsRecurring ───────────────────────────────────────────────────────────────

func TestIsRecurring_Empty(t *testing.T) {
	task := &Task{}
	if task.IsRecurring() {
		t.Error("task with no Recurrence should not be recurring")
	}
}

func TestIsRecurring_WithValue(t *testing.T) {
	for _, r := range []string{"daily", "weekly", "biweekly", "monthly"} {
		task := &Task{Recurrence: r}
		if !task.IsRecurring() {
			t.Errorf("task with Recurrence=%q should be recurring", r)
		}
	}
}

// ── DaysInColumn ──────────────────────────────────────────────────────────────

func TestDaysInColumn(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		createdAt time.Time
		want      int
	}{
		{
			name:      "created today (midnight)",
			createdAt: today,
			want:      0,
		},
		{
			name:      "created today (now)",
			createdAt: now,
			want:      0,
		},
		{
			name:      "created 1 day ago",
			createdAt: today.AddDate(0, 0, -1),
			want:      1,
		},
		{
			name:      "created 3 days ago",
			createdAt: today.AddDate(0, 0, -3),
			want:      3,
		},
		{
			name:      "created 10 days ago",
			createdAt: today.AddDate(0, 0, -10),
			want:      10,
		},
		{
			name:      "created 6 days ago (just before a week)",
			createdAt: today.AddDate(0, 0, -6),
			want:      6,
		},
		{
			name:      "created 7 days ago",
			createdAt: today.AddDate(0, 0, -7),
			want:      7,
		},
		{
			// Guard: future CreatedAt (clock skew) should not go negative.
			name:      "created in the future",
			createdAt: today.AddDate(0, 0, 1),
			want:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := &Task{CreatedAt: tc.createdAt}
			if got := task.DaysInColumn(); got != tc.want {
				t.Errorf("DaysInColumn() = %d, want %d", got, tc.want)
			}
		})
	}
}

// ── AgeClass ──────────────────────────────────────────────────────────────────

func TestAgeClass(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		createdAt time.Time
		want      string
	}{
		{"0 days → fresh",  today,                    "age-fresh"},
		{"1 day → fresh",   today.AddDate(0, 0, -1),  "age-fresh"},
		{"2 days → warn",   today.AddDate(0, 0, -2),  "age-warn"},
		{"3 days → warn",   today.AddDate(0, 0, -3),  "age-warn"},
		{"5 days → warn",   today.AddDate(0, 0, -5),  "age-warn"},
		{"6 days → stale",  today.AddDate(0, 0, -6),  "age-stale"},
		{"10 days → stale", today.AddDate(0, 0, -10), "age-stale"},
		{"30 days → stale", today.AddDate(0, 0, -30), "age-stale"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := &Task{CreatedAt: tc.createdAt}
			if got := task.AgeClass(); got != tc.want {
				t.Errorf("AgeClass() = %q, want %q (days=%d)",
					got, tc.want, task.DaysInColumn())
			}
		})
	}
}

// ── AgeLabel ──────────────────────────────────────────────────────────────────

func TestAgeLabel(t *testing.T) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		createdAt time.Time
		want      string
	}{
		{"0 days",  today,                    "0d"},
		{"1 day",   today.AddDate(0, 0, -1),  "1d"},
		{"6 days",  today.AddDate(0, 0, -6),  "6d"},
		{"7 days",  today.AddDate(0, 0, -7),  "1w"},
		{"8 days",  today.AddDate(0, 0, -8),  "1w"},
		{"13 days", today.AddDate(0, 0, -13), "1w"},
		{"14 days", today.AddDate(0, 0, -14), "2w"},
		{"21 days", today.AddDate(0, 0, -21), "3w"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := &Task{CreatedAt: tc.createdAt}
			if got := task.AgeLabel(); got != tc.want {
				t.Errorf("AgeLabel() = %q, want %q (days=%d)",
					got, tc.want, task.DaysInColumn())
			}
		})
	}
}
