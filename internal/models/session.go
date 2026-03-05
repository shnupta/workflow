package models

import "time"

// SessionMode controls how the session runs.
type SessionMode string

const (
	SessionModeInteractive SessionMode = "interactive"
)

// SessionStatus is the lifecycle state of a session.
type SessionStatus string

const (
	SessionStatusPending     SessionStatus = "pending"
	SessionStatusRunning     SessionStatus = "running"
	SessionStatusIdle        SessionStatus = "idle"        // interactive, waiting for input
	SessionStatusComplete    SessionStatus = "complete"
	SessionStatusError       SessionStatus = "error"
	SessionStatusInterrupted SessionStatus = "interrupted" // cancelled by user mid-run
)

// Session represents an agent session attached to a task.
type Session struct {
	ID             string        `db:"id"              json:"id"`
	TaskID         string        `db:"task_id"         json:"task_id"`
	ParentID       *string       `db:"parent_id"       json:"parent_id"`
	Name           string        `db:"name"            json:"name"`
	Mode           SessionMode   `db:"mode"            json:"mode"`
	Status         SessionStatus `db:"status"          json:"status"`
	AgentProvider  string        `db:"agent_provider"  json:"agent_provider"`
	AgentSessionID *string       `db:"agent_session_id" json:"agent_session_id"`
	ErrorMessage   string        `db:"error_message"   json:"error_message"`
	Archived       bool          `db:"archived"        json:"archived"`
	Pinned         bool          `db:"pinned"          json:"pinned"`
	CreatedAt      time.Time     `db:"created_at"      json:"created_at"`
	UpdatedAt      time.Time     `db:"updated_at"      json:"updated_at"`
}

// MessageRole is the sender role for a message.
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
	MessageRoleSystem    MessageRole = "system"
)

// MessageKind distinguishes the type of content in a message.
type MessageKind string

const (
	MessageKindText       MessageKind = "text"
	MessageKindToolUse    MessageKind = "tool_use"
	MessageKindToolResult MessageKind = "tool_result"
	MessageKindThinking   MessageKind = "thinking"
	MessageKindError      MessageKind = "error"
	MessageKindContext    MessageKind = "context" // injected task context, shown as collapsible info block
)

// SessionWithTask is a Session enriched with the parent task title for list views.
type SessionWithTask struct {
	Session
	TaskTitle string `db:"title" json:"task_title"`
}

// Message is a single turn in a session's conversation.
// Content is always plain text or markdown — provider-specific formats
// are normalised before storage.
type Message struct {
	ID         string      `db:"id"         json:"id"`
	SessionID  string      `db:"session_id" json:"session_id"`
	Role       MessageRole `db:"role"       json:"role"`
	Kind       MessageKind `db:"kind"       json:"kind"`
	Content    string      `db:"content"    json:"content"`
	ToolName   string      `db:"tool_name"  json:"tool_name"`
	Metadata   string      `db:"metadata"   json:"metadata"`
	CreatedAt  time.Time   `db:"created_at" json:"created_at"`
}
