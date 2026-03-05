// Package agent defines the provider-agnostic agent interface and normalised
// event types. Each concrete provider (e.g. Claude local CLI, Claude API)
// implements Runner and emits Events that workflow stores in its own schema.
package agent

import "context"

// ─────────────────────────────────────────────────────────
// Normalised event types — nothing provider-specific here
// ─────────────────────────────────────────────────────────

// EventKind classifies what happened in a turn.
type EventKind string

const (
	EventText        EventKind = "text"         // assistant said something
	EventThinking    EventKind = "thinking"     // assistant thinking block
	EventToolUse     EventKind = "tool_use"     // agent called a tool
	EventToolResult  EventKind = "tool_result"  // tool returned a result
	EventSubagent    EventKind = "subagent"     // a sub-agent was spawned
	EventDone        EventKind = "done"         // session completed successfully
	EventError       EventKind = "error"        // session errored
)

// Event is a single normalised event emitted by a Runner.
type Event struct {
	Kind EventKind

	// Text / Thinking
	Content string

	// Tool use
	ToolName  string
	ToolInput string // JSON string

	// Tool result
	ToolResult string

	// Sub-agent (provider session ID of child, so caller can track nesting)
	SubagentID   string
	SubagentName string

	// Done
	ExitCode int

	// Error
	Err error

	// Provider-native session ID — set on first event, used for resuming
	ProviderSessionID string

	// Parent tool use ID — set on events from sub-agents, identifies which
	// tool call spawned them. Callers use this to build the nesting tree.
	ParentToolUseID string
}

// ─────────────────────────────────────────────────────────
// Runner interface
// ─────────────────────────────────────────────────────────

// RunOptions configures a single agent run.
type RunOptions struct {
	Prompt string

	// Optional: provider-native session ID to resume a previous session.
	ResumeSessionID string

	// Optional: working directory for the agent.
	WorkDir string

	// Optional: extra env vars.
	Env []string
}

// Runner starts an agent and streams normalised Events until done or ctx cancelled.
// Implementations must close the returned channel when the run ends.
type Runner interface {
	// Name returns a short identifier for this provider (e.g. "claude_local").
	Name() string

	// Run starts the agent and returns a channel of Events.
	// The channel is closed when the run completes or errors.
	Run(ctx context.Context, opts RunOptions) (<-chan Event, error)
}
