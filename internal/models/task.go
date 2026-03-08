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
	Tags         []string   `db:"-"              json:"tags"`          // populated by GetTask / ListTasks (not a column)
}

// IsBlocked returns true when the task has an active blocker set.
func (t *Task) IsBlocked() bool { return t.BlockedBy != "" }

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
