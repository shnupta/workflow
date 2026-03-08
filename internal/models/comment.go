package models

import "time"

// Comment is a timestamped log entry attached to a specific task.
type Comment struct {
	ID        int64     `db:"id"         json:"id"`
	TaskID    string    `db:"task_id"    json:"task_id"`
	Body      string    `db:"body"       json:"body"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// FormattedTime returns a human-readable timestamp in the form "Jan 2, 3:04pm".
func (c *Comment) FormattedTime() string {
	return c.CreatedAt.Format("Jan 2, 3:04pm")
}
