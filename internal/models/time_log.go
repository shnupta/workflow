package models

import "time"

// TimeLog records a unit of time spent on a task.
type TimeLog struct {
	ID           string    `db:"id"            json:"id"`
	TaskID       string    `db:"task_id"       json:"task_id"`
	LoggedAt     time.Time `db:"logged_at"     json:"logged_at"`
	DurationMins int       `db:"duration_mins" json:"duration_mins"`
	Note         string    `db:"note"          json:"note"`
}

// FormattedTime returns a human-friendly timestamp for templates.
func (l TimeLog) FormattedTime() string {
	return l.LoggedAt.Local().Format("Jan 2, 3:04 PM")
}
