package models

import "time"

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
	DueDate     *time.Time `db:"due_date"      json:"due_date"` // optional due date (date only, no time)
}

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
