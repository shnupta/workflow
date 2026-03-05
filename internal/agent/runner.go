package agent

import (
	"context"
	"log"
	"time"

	"github.com/shnupta/workflow/internal/models"
)

// DB is the minimal interface the runner needs from the database layer.
type DB interface {
	CreateMessage(m *models.Message) error
	UpdateSessionStatus(id string, status models.SessionStatus, errMsg string) error
	UpdateSessionAgentID(id, agentSessionID string) error
}

// RunSession starts an agent session in the background, writing normalised
// messages to the DB as events arrive. Safe to call in a goroutine.
// promptForAgent is what the agent receives (may include injected context).
// visiblePrompt is what gets stored as the user message in the chat (just the user's actual words).
// If visiblePrompt is empty, promptForAgent is used for both.
func RunSession(ctx context.Context, db DB, sess *models.Session, runner Runner, promptForAgent string, visiblePrompt ...string) {
	if err := db.UpdateSessionStatus(sess.ID, models.SessionStatusRunning, ""); err != nil {
		log.Printf("runner: update status running: %v", err)
	}

	opts := RunOptions{
		Prompt: promptForAgent,
	}
	if sess.AgentSessionID != nil {
		opts.ResumeSessionID = *sess.AgentSessionID
	}

	// Store the visible user prompt as the first message
	displayPrompt := promptForAgent
	if len(visiblePrompt) > 0 && visiblePrompt[0] != "" {
		displayPrompt = visiblePrompt[0]
	}
	_ = db.CreateMessage(&models.Message{
		SessionID: sess.ID,
		Role:      models.MessageRoleUser,
		Kind:      models.MessageKindText,
		Content:   displayPrompt,
		CreatedAt: time.Now(),
	})

	ch, err := runner.Run(ctx, opts)
	if err != nil {
		log.Printf("runner: start agent: %v", err)
		_ = db.UpdateSessionStatus(sess.ID, models.SessionStatusError, err.Error())
		_ = db.CreateMessage(&models.Message{
			SessionID: sess.ID,
			Role:      models.MessageRoleSystem,
			Kind:      models.MessageKindError,
			Content:   err.Error(),
			CreatedAt: time.Now(),
		})
		return
	}

	for evt := range ch {
		// Update provider session ID on first event that carries one
		if evt.ProviderSessionID != "" && (sess.AgentSessionID == nil || *sess.AgentSessionID == "") {
			sid := evt.ProviderSessionID
			sess.AgentSessionID = &sid
			_ = db.UpdateSessionAgentID(sess.ID, sid)
		}

		msg := eventToMessage(sess.ID, evt)
		if msg != nil {
			if err := db.CreateMessage(msg); err != nil {
				log.Printf("runner: save message: %v", err)
			}
		}

		switch evt.Kind {
		case EventDone:
			_ = db.UpdateSessionStatus(sess.ID, models.SessionStatusComplete, "")
			return
		case EventError:
			errMsg := ""
			if evt.Err != nil {
				errMsg = evt.Err.Error()
			}
			_ = db.UpdateSessionStatus(sess.ID, models.SessionStatusError, errMsg)
			return
		}
	}

	// Channel closed without explicit done/error — treat as complete
	_ = db.UpdateSessionStatus(sess.ID, models.SessionStatusComplete, "")
}

func eventToMessage(sessionID string, evt Event) *models.Message {
	m := &models.Message{
		SessionID: sessionID,
		CreatedAt: time.Now(),
	}

	switch evt.Kind {
	case EventText:
		if evt.Content == "" {
			return nil // skip empty init events
		}
		m.Role = models.MessageRoleAssistant
		m.Kind = models.MessageKindText
		m.Content = evt.Content

	case EventThinking:
		m.Role = models.MessageRoleAssistant
		m.Kind = models.MessageKindThinking
		m.Content = evt.Content

	case EventToolUse:
		m.Role = models.MessageRoleAssistant
		m.Kind = models.MessageKindToolUse
		m.ToolName = evt.ToolName
		m.Content = evt.ToolInput

	case EventToolResult:
		m.Role = models.MessageRoleTool
		m.Kind = models.MessageKindToolResult
		m.ToolName = evt.ToolName
		m.Content = evt.ToolResult

	case EventError:
		m.Role = models.MessageRoleSystem
		m.Kind = models.MessageKindError
		if evt.Err != nil {
			m.Content = evt.Err.Error()
		}

	case EventDone:
		// Don't store a "[Session complete]" message — status change is enough.
		return nil

	default:
		return nil
	}

	return m
}
