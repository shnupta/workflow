package models

import (
	"strings"
	"time"
)

// Note is a freeform markdown scratchpad entry.
// Notes can be global (task_id = "") or attached to a specific task.
type Note struct {
	ID        string    `db:"id"         json:"id"`
	TaskID    string    `db:"task_id"    json:"task_id"`    // "" for global notes
	Title     string    `db:"title"      json:"title"`      // first line of content, auto-derived
	Content   string    `db:"content"    json:"content"`    // full markdown
	TagsRaw   string    `db:"tags"       json:"tags_raw"`   // comma-separated tag string
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// Tags returns the note's tags as a slice (splits TagsRaw on comma).
func (n *Note) Tags() []string {
	if n.TagsRaw == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(n.TagsRaw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
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
