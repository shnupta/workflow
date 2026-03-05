package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"net/http"
	"time"

	"github.com/shnupta/workflow/internal/agent"
	"github.com/shnupta/workflow/internal/models"
)

// registerSessionRoutes adds session endpoints to the mux.
func (h *Handler) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /tasks/{id}/sessions", h.createSession)
	mux.HandleFunc("GET /tasks/{id}/sessions", h.listSessions)
	mux.HandleFunc("GET /tasks/{id}/sessions/{sid}", h.viewSession)
	mux.HandleFunc("GET /tasks/{id}/sessions/{sid}/messages", h.getMessages)
	mux.HandleFunc("POST /tasks/{id}/sessions/{sid}/messages", h.sendMessage)
}

// createSession starts a new agent session on a task.
// Body: {"prompt": "...", "name": "...", "mode": "interactive"}
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	task, err := h.db.GetTask(taskID)
	if err != nil {
		http.Error(w, "task not found", 404)
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
		Name   string `json:"name"`
		Mode   string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if body.Prompt == "" {
		http.Error(w, "prompt required", 400)
		return
	}
	name := body.Name
	if name == "" {
		name = sessionNameFromPrompt(body.Prompt)
	}

	sess := &models.Session{
		TaskID:        taskID,
		Name:          name,
		Mode:          models.SessionModeInteractive,
		Status:        models.SessionStatusPending,
		AgentProvider: "claude_local",
	}
	if err := h.db.CreateSession(sess); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Validate claude binary is reachable before creating the session
	runner := &agent.ClaudeLocal{ClaudeBin: h.cfg().ClaudeBin}
	if err := runner.Validate(); err != nil {
		_ = h.db.UpdateSessionStatus(sess.ID, models.SessionStatusError, err.Error())
		http.Error(w, "claude CLI not available: "+err.Error(), 503)
		return
	}

	// Build the full prompt (context + user message) for the agent,
	// but store the context and user message as separate DB messages so
	// the chat UI can render them differently.
	contextBlock := buildTaskContext(task)
	fullPrompt := contextBlock + "\n---\n\n" + body.Prompt

	// Store context as a collapsible info block (not a chat bubble)
	_ = h.db.CreateMessage(&models.Message{
		SessionID: sess.ID,
		Role:      models.MessageRoleSystem,
		Kind:      models.MessageKindContext,
		Content:   contextBlock,
		CreatedAt: time.Now(),
	})

	// Start runner — pass full prompt to agent, but only store user's actual words in chat
	go agent.RunSession(context.Background(), h.db, sess, runner, fullPrompt, body.Prompt)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": sess.ID})
}

// listSessions returns all sessions for a task as JSON.
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	sessions, err := h.db.ListSessions(taskID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

// viewSession renders the session chat page.
func (h *Handler) viewSession(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	sid := r.PathValue("sid")

	task, err := h.db.GetTask(taskID)
	if err != nil {
		http.Error(w, "task not found", 404)
		return
	}
	sess, err := h.db.GetSession(sid)
	if err != nil || sess.TaskID != taskID {
		http.Error(w, "session not found", 404)
		return
	}
	messages, err := h.db.ListMessages(sid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	h.render(w, "session.html", map[string]interface{}{
		"Task":     task,
		"Session":  sess,
		"Messages": messages,
	})
}

// getMessages returns messages for a session as JSON, optionally filtered
// to only those after a given message ID (for polling).
// Query: ?after=<message_id>
func (h *Handler) getMessages(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	sid := r.PathValue("sid")

	sess, err := h.db.GetSession(sid)
	if err != nil || sess.TaskID != taskID {
		http.Error(w, "session not found", 404)
		return
	}

	afterID := r.URL.Query().Get("after")
	msgs, err := h.db.ListMessagesSince(sid, afterID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Also return the current session status so the client knows when to stop polling
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   sess.Status,
		"messages": msgs,
	})
}

// sendMessage adds a user message and continues an interactive session.
func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	sid := r.PathValue("sid")

	sess, err := h.db.GetSession(sid)
	if err != nil || sess.TaskID != taskID {
		http.Error(w, "session not found", 404)
		return
	}
	if sess.Mode != models.SessionModeInteractive {
		http.Error(w, "session is not interactive", 400)
		return
	}
	switch sess.Status {
	case models.SessionStatusIdle, models.SessionStatusComplete:
		// OK — agent is free to accept the next message
	default:
		http.Error(w, "session is busy (status: "+string(sess.Status)+")", 409)
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
		http.Error(w, "prompt required", 400)
		return
	}

	runner := &agent.ClaudeLocal{ClaudeBin: h.cfg().ClaudeBin}
	go agent.RunSession(context.Background(), h.db, sess, runner, body.Prompt)

	w.WriteHeader(http.StatusAccepted)
}

// sessionNameFromPrompt generates a short session name from the first few words of the prompt.
func sessionNameFromPrompt(prompt string) string {
	words := strings.Fields(prompt)
	if len(words) == 0 {
		return "Session"
	}
	if len(words) > 6 {
		words = words[:6]
	}
	name := strings.Join(words, " ")
	if len(name) > 48 {
		name = name[:45] + "..."
	}
	return name
}

// buildTaskContext returns the task context block sent to the agent.
func buildTaskContext(t *models.Task) string {
	var b strings.Builder
	b.WriteString("## Task context\n")
	b.WriteString("You are working on the following task. Use this context to inform your work.\n\n")
	b.WriteString("**Title:** " + t.Title + "\n")
	if t.Description != "" {
		b.WriteString("**Description:** " + t.Description + "\n")
	}
	b.WriteString("**Type:** " + t.WorkType + "\n")
	if t.PRURL != "" {
		b.WriteString("**PR URL:** " + t.PRURL + "\n")
	}
	if t.Link != "" {
		b.WriteString("**Link:** " + t.Link + "\n")
	}
	if t.Brief != "" && t.BriefStatus == "done" {
		b.WriteString("\n**Preliminary investigation:**\n")
		b.WriteString(t.Brief)
		b.WriteString("\n")
	}
	return b.String()
}
