package models

import "time"

// Note is a freeform markdown scratchpad entry.
// Notes can be global (task_id = "") or attached to a specific task.
type Note struct {
	ID        string    `db:"id"         json:"id"`
	TaskID    string    `db:"task_id"    json:"task_id"`    // "" for global notes
	Title     string    `db:"title"      json:"title"`      // first line of content, auto-derived
	Content   string    `db:"content"    json:"content"`    // full markdown
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// DisplayTitle returns either the explicit title or a truncated content preview.
func (n *Note) DisplayTitle() string {
	if n.Title != "" {
		return n.Title
	}
	if len(n.Content) > 60 {
		return n.Content[:57] + "..."
	}
	if n.Content == "" {
		return "Untitled note"
	}
	return n.Content
}
