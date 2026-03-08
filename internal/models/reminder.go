package models

import "time"

// Reminder is a scheduled notification attached to a task. When remind_at
// passes and sent=false, the check_reminders script fires a Telegram message
// and marks it sent.
type Reminder struct {
	ID        int64     `db:"id"         json:"id"`
	TaskID    string    `db:"task_id"    json:"task_id"`
	RemindAt  time.Time `db:"remind_at"  json:"remind_at"`
	Note      string    `db:"note"       json:"note"`
	Sent      bool      `db:"sent"       json:"sent"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// RemindAtFormatted returns a human-readable time in the form "Mar 9, 9:00am".
func (r *Reminder) RemindAtFormatted() string {
	return r.RemindAt.Format("Jan 2, 3:04pm")
}

// IsPast returns true if the reminder time has already passed.
func (r *Reminder) IsPast() bool {
	return time.Now().After(r.RemindAt)
}
