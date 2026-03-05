package handlers

import (
	"context"
	"encoding/json"
	"fmt"
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
// Body: {"prompt": "...", "name": "...", "mode": "fire_and_forget|interactive"}
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, err := h.db.GetTask(taskID); err != nil {
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
	mode := models.SessionModeFireAndForget
	if body.Mode == string(models.SessionModeInteractive) {
		mode = models.SessionModeInteractive
	}
	name := body.Name
	if name == "" {
		name = fmt.Sprintf("Session %s", time.Now().Format("Jan 2 15:04"))
	}

	sess := &models.Session{
		TaskID:        taskID,
		Name:          name,
		Mode:          mode,
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
		// Clean up the session we just created
		_ = h.db.UpdateSessionStatus(sess.ID, models.SessionStatusError, err.Error())
		http.Error(w, "claude CLI not available: "+err.Error(), 503)
		return
	}

	// Start runner in background
	go agent.RunSession(context.Background(), h.db, sess, runner, body.Prompt)

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

	runner := &agent.ClaudeLocal{}
	go agent.RunSession(context.Background(), h.db, sess, runner, body.Prompt)

	w.WriteHeader(http.StatusAccepted)
}
