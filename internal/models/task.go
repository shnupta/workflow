package models

import "time"

type Task struct {
	ID          string     `db:"id"`
	Title       string     `db:"title"`
	Description string     `db:"description"`
	WorkType    string     `db:"work_type"`
	Tier        string     `db:"tier"`
	Direction   string     `db:"direction"` // "blocked_on_me" | "blocked_on_them"
	PRURL       string     `db:"pr_url"`
	PRSummary   string     `db:"pr_summary"`
	Link        string     `db:"link"`
	Done        bool       `db:"done"`
	Position    int        `db:"position"` // sort order within tier
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	DoneAt      *time.Time `db:"done_at"`
}

func (t *Task) DirectionLabel() string {
	if t.Direction == "blocked_on_them" {
		return "On them"
	}
	return "On me"
}
