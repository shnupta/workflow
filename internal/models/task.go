package models

import (
	"fmt"
	"time"
)

type Task struct {
	ID          string     `db:"id"           json:"id"`
	Title       string     `db:"title"         json:"title"`
	Description string     `db:"description"   json:"description"`
	WorkType    string     `db:"work_type"     json:"work_type"`
	Tier        string     `db:"tier"          json:"tier"`
	Direction   string     `db:"direction"     json:"direction"` // "blocked_on_me" | "blocked_on_them"
	PRURL       string     `db:"pr_url"        json:"pr_url"`
	Brief       string     `db:"brief"         json:"brief"`        // auto-analysis brief from agent
	BriefStatus string     `db:"brief_status"  json:"brief_status"` // "" | "pending" | "done" | "error"
	Link        string     `db:"link"          json:"link"`
	Done        bool       `db:"done"          json:"done"`
	Archived    bool       `db:"archived"      json:"archived"`
	Position    int        `db:"position"      json:"position"`
	CreatedAt   time.Time  `db:"created_at"    json:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"    json:"updated_at"`
	DoneAt      *time.Time `db:"done_at"       json:"done_at"`
	DueDate      *time.Time `db:"due_date"       json:"due_date"`      // optional due date (date only, no time)
	TimerStarted *time.Time `db:"timer_started"  json:"timer_started"` // non-nil when timer is running
	TimerTotal   int        `db:"timer_total"    json:"timer_total"`   // accumulated seconds (not counting current run)
	Scratchpad   string     `db:"scratchpad"     json:"scratchpad"`    // free-form notes/context for this task
	BlockedBy    string     `db:"blocked_by"     json:"blocked_by"`    // ID of the task blocking this one (empty = not blocked)
	Recurrence   string     `db:"recurrence"     json:"recurrence"`    // "" | "daily" | "weekly" | "biweekly" | "monthly"
	Priority     string     `db:"priority"       json:"priority"`      // "" | "p1" | "p2" | "p3"
	Effort       string     `db:"effort"         json:"effort"`        // "" | "xs" | "s" | "m" | "l" | "xl"
	Starred      bool       `db:"starred"        json:"starred"`       // pinned to top of board
	Tags         []string   `db:"-"              json:"tags"`          // populated by GetTask / ListTasks (not a column)
	CommentCount int        `db:"-"              json:"comment_count"` // populated by ListTasks (not a column)
}

// IsBlocked returns true when the task has an active blocker set.
func (t *Task) IsBlocked() bool { return t.BlockedBy != "" }

// DaysInColumn returns the number of complete calendar days the task has been
// in its current column, measured from created_at to now (UTC, day-truncated).
// A task created earlier today returns 0.
func (t *Task) DaysInColumn() int {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	created := t.CreatedAt.UTC()
	start := time.Date(created.Year(), created.Month(), created.Day(), 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(start).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// AgeLabel returns a compact age string: "0d"–"6d" for the first week, then
// whole weeks ("1w", "2w", …).
func (t *Task) AgeLabel() string {
	d := t.DaysInColumn()
	if d < 7 {
		return fmt.Sprintf("%dd", d)
	}
	return fmt.Sprintf("%dw", d/7)
}

// AgeClass returns a CSS class name reflecting how long the task has been in
// its column: "age-fresh" (< 2 days), "age-warn" (2–5 days), "age-stale" (≥ 6 days).
func (t *Task) AgeClass() string {
	switch d := t.DaysInColumn(); {
	case d < 2:
		return "age-fresh"
	case d <= 5:
		return "age-warn"
	default:
		return "age-stale"
	}
}

// IsRecurring returns true when the task has a recurrence schedule set.
func (t *Task) IsRecurring() bool { return t.Recurrence != "" }

func (t *Task) DirectionLabel() string {
	if t.Direction == "blocked_on_them" {
		return "On them"
	}
	return "On me"
}

// IsOverdue returns true if the task has a due date in the past and is not done.
func (t *Task) IsOverdue() bool {
	if t.DueDate == nil || t.Done {
		return false
	}
	// Compare dates only (truncate to day)
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	due := time.Date(t.DueDate.Year(), t.DueDate.Month(), t.DueDate.Day(), 0, 0, 0, 0, now.Location())
	return due.Before(today)
}

// IsDueToday returns true if the task is due today.
func (t *Task) IsDueToday() bool {
	if t.DueDate == nil || t.Done {
		return false
	}
	now := time.Now()
	return t.DueDate.Year() == now.Year() && t.DueDate.Month() == now.Month() && t.DueDate.Day() == now.Day()
}

// ElapsedSeconds returns total elapsed seconds including any current run.
func (t *Task) ElapsedSeconds() int {
	total := t.TimerTotal
	if t.TimerStarted != nil {
		total += int(time.Since(*t.TimerStarted).Seconds())
	}
	return total
}

// ElapsedLabel formats elapsed time as "1h 23m" or "45m" or "< 1m".
func (t *Task) ElapsedLabel() string {
	secs := t.ElapsedSeconds()
	if secs < 60 {
		if secs == 0 {
			return ""
		}
		return "< 1m"
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// PriorityLabel returns a human-readable label for the task's priority.
func (t *Task) PriorityLabel() string {
	switch t.Priority {
	case "p1":
		return "P1"
	case "p2":
		return "P2"
	case "p3":
		return "P3"
	default:
		return ""
	}
}

// PriorityWeight returns a sort weight for priority (lower = higher priority).
// Used for board card ordering: p1=0, p2=1, p3=2, ""=3.
func (t *Task) PriorityWeight() int {
	switch t.Priority {
	case "p1":
		return 0
	case "p2":
		return 1
	case "p3":
		return 2
	default:
		return 3
	}
}

// EffortLabel returns a display label for the task's effort estimate.
func (t *Task) EffortLabel() string {
	switch t.Effort {
	case "xs":
		return "XS"
	case "s":
		return "S"
	case "m":
		return "M"
	case "l":
		return "L"
	case "xl":
		return "XL"
	default:
		return ""
	}
}

// EffortPoints returns a rough story-points equivalent for effort estimates.
// xs=1, s=2, m=3, l=5, xl=8 (Fibonacci-ish).
func (t *Task) EffortPoints() int {
	switch t.Effort {
	case "xs":
		return 1
	case "s":
		return 2
	case "m":
		return 3
	case "l":
		return 5
	case "xl":
		return 8
	default:
		return 0
	}
}
