package models

import "time"

// SessionMode controls how the session runs.
type SessionMode string

const (
	SessionModeFireAndForget SessionMode = "fire_and_forget"
	SessionModeInteractive   SessionMode = "interactive"
)

// SessionStatus is the lifecycle state of a session.
type SessionStatus string

const (
	SessionStatusPending  SessionStatus = "pending"
	SessionStatusRunning  SessionStatus = "running"
	SessionStatusIdle     SessionStatus = "idle"     // interactive, waiting for input
	SessionStatusComplete SessionStatus = "complete"
	SessionStatusError    SessionStatus = "error"
)

// Session represents an agent session attached to a task.
type Session struct {
	ID             string        `db:"id"`
	TaskID         string        `db:"task_id"`
	ParentID       *string       `db:"parent_id"`       // nullable — for sub-agent nesting
	Name           string        `db:"name"`
	Mode           SessionMode   `db:"mode"`
	Status         SessionStatus `db:"status"`
	AgentProvider  string        `db:"agent_provider"`  // e.g. "claude_local", "claude_api"
	AgentSessionID *string       `db:"agent_session_id"` // provider-native session ID for resuming
	ErrorMessage   string        `db:"error_message"`
	CreatedAt      time.Time     `db:"created_at"`
	UpdatedAt      time.Time     `db:"updated_at"`
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
)

// Message is a single turn in a session's conversation.
// Content is always plain text or markdown — provider-specific formats
// are normalised before storage.
type Message struct {
	ID         string      `db:"id"`
	SessionID  string      `db:"session_id"`
	Role       MessageRole `db:"role"`
	Kind       MessageKind `db:"kind"`
	Content    string      `db:"content"`  // normalised text content
	ToolName   string      `db:"tool_name"` // set for tool_use / tool_result kinds
	Metadata   string      `db:"metadata"`  // JSON blob for provider-specific extras
	CreatedAt  time.Time   `db:"created_at"`
}
