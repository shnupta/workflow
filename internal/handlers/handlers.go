package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/shnupta/workflow/internal/agent"
	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

type Handler struct {
	db       *db.DB
	watcher  *config.Watcher
	tmplGlob string
	templates *template.Template
}

func (h *Handler) cfg() *config.Config { return h.watcher.Get() }

func New(d *db.DB, watcher *config.Watcher, tmplGlob string) (*Handler, error) {
	h := &Handler{db: d, watcher: watcher, tmplGlob: tmplGlob}

	funcMap := template.FuncMap{
		"workTypeDepth": func(key string) string { return h.cfg().WorkTypeDepth(key) },
		"timeAgo":       timeAgo,
		"markdownHTML": func(s string) template.HTML {
			var buf bytes.Buffer
			if err := goldmark.Convert([]byte(s), &buf); err != nil {
				return template.HTML(template.HTMLEscapeString(s))
			}
			return template.HTML(buf.String())
		},
		"workTypeLabel": func(key string) string {
			if wt := h.cfg().WorkTypeByKey(key); wt != nil {
				return wt.Label
			}
			return key
		},
		"tierLabel": func(key string) string {
			if t := h.cfg().TierByKey(key); t != nil {
				return t.Label
			}
			return key
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseGlob(tmplGlob)
	if err != nil {
		return nil, err
	}
	h.templates = tmpl
	return h, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("GET /tasks/new", h.newTaskForm)
	mux.HandleFunc("POST /tasks", h.createTask)
	mux.HandleFunc("GET /tasks/{id}", h.viewTask)
	mux.HandleFunc("GET /tasks/{id}/edit", h.editTaskForm)
	mux.HandleFunc("POST /tasks/{id}", h.updateTask)
	mux.HandleFunc("POST /tasks/{id}/done", h.markDone)
	mux.HandleFunc("POST /tasks/{id}/move", h.moveTask)
	mux.HandleFunc("DELETE /tasks/{id}", h.deleteTask)
	mux.HandleFunc("POST /tasks/{id}/rebrief", h.rebrief)
	mux.HandleFunc("GET /tasks/{id}/brief-status", h.briefStatus)
	mux.HandleFunc("GET /sessions", h.sessionsIndex)
	mux.HandleFunc("GET /notes", h.notesPage)
	h.registerSessionRoutes(mux)
}

func (h *Handler) sessionsIndex(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.db.ListAllSessions()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Group by task_id for the grouped view
	type group struct {
		TaskID    string
		TaskTitle string
		Sessions  []*models.SessionWithTask
	}
	groupMap := make(map[string]*group)
	var groupOrder []string
	for _, s := range sessions {
		if _, ok := groupMap[s.TaskID]; !ok {
			groupMap[s.TaskID] = &group{TaskID: s.TaskID, TaskTitle: s.TaskTitle}
			groupOrder = append(groupOrder, s.TaskID)
		}
		groupMap[s.TaskID].Sessions = append(groupMap[s.TaskID].Sessions, s)
	}
	groups := make([]*group, 0, len(groupOrder))
	for _, id := range groupOrder {
		groups = append(groups, groupMap[id])
	}
	h.render(w, "sessions_index.html", map[string]interface{}{
		"Sessions": sessions,
		"Groups":   groups,
		"Nav":      "sessions",
	})
}

func (h *Handler) notesPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "notes.html", map[string]interface{}{
		"Nav": "notes",
	})
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.db.ListTasks(false, h.cfg())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	grouped := make(map[string][]*models.Task)
	for _, t := range tasks {
		grouped[t.Tier] = append(grouped[t.Tier], t)
	}

	tiers := make([]map[string]interface{}, len(h.cfg().Tiers))
	for i, tier := range h.cfg().Tiers {
		tiers[i] = map[string]interface{}{
			"Key":   tier.Key,
			"Label": tier.Label,
			"Tasks": grouped[tier.Key],
		}
	}

	h.render(w, "index.html", map[string]interface{}{
		"Tiers":     tiers,
		"WorkTypes": h.cfg().WorkTypes,
	})
}

func (h *Handler) newTaskForm(w http.ResponseWriter, r *http.Request) {
	defaultTier := ""
	if len(h.cfg().Tiers) > 0 {
		defaultTier = h.cfg().Tiers[0].Key
	}
	h.render(w, "task_form.html", map[string]interface{}{
		"WorkTypes": h.cfg().WorkTypes,
		"Tiers":     h.cfg().Tiers,
		"Task":      &models.Task{Tier: defaultTier, Direction: "blocked_on_me"},
		"IsNew":     true,
	})
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t := &models.Task{
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		WorkType:    r.FormValue("work_type"),
		Tier:        r.FormValue("tier"),
		Direction:   r.FormValue("direction"),
		PRURL:       r.FormValue("pr_url"),
		Link:        r.FormValue("link"),
	}
	if err := h.db.CreateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Kick off auto-brief in background
	go h.runAutoBrief(t)

	http.Redirect(w, r, "/tasks/"+t.ID, http.StatusSeeOther)
}

func (h *Handler) viewTask(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	sessions, _ := h.db.ListSessions(t.ID)
	messages, _ := h.db.ListMessages(t.ID) // for task-level messages (unused for now)
	_ = messages
	h.render(w, "task_view.html", map[string]interface{}{
		"Task":     t,
		"Sessions": sessions,
	})
}

func (h *Handler) editTaskForm(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	h.render(w, "task_form.html", map[string]interface{}{
		"WorkTypes": h.cfg().WorkTypes,
		"Tiers":     h.cfg().Tiers,
		"Task":      t,
		"IsNew":     false,
	})
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t.Title = r.FormValue("title")
	t.Description = r.FormValue("description")
	t.WorkType = r.FormValue("work_type")
	t.Tier = r.FormValue("tier")
	t.Direction = r.FormValue("direction")
	t.PRURL = r.FormValue("pr_url")
	t.Link = r.FormValue("link")
	if err := h.db.UpdateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tasks/"+t.ID, http.StatusSeeOther)
}

func (h *Handler) markDone(w http.ResponseWriter, r *http.Request) {
	if err := h.db.MarkDone(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) moveTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tier     string `json:"tier"`
		BeforeID string `json:"before_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := h.db.MoveTask(r.PathValue("id"), body.Tier, body.BeforeID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteTask(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rebrief re-runs the auto-brief agent for a task.
func (h *Handler) rebrief(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if err := h.db.UpdateBrief(t.ID, "", "pending"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	t.BriefStatus = "pending"
	go h.runAutoBrief(t)
	w.WriteHeader(http.StatusAccepted)
}

// briefStatus returns the current brief as JSON for polling.
func (h *Handler) briefStatus(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch t.BriefStatus {
	case "done":
		fmt.Fprintf(w, `{"status":"done","brief":%s}`, jsonStr(t.Brief))
	case "error":
		fmt.Fprintf(w, `{"status":"error","brief":%s}`, jsonStr(t.Brief))
	case "pending":
		fmt.Fprintf(w, `{"status":"pending"}`)
	default:
		fmt.Fprintf(w, `{"status":"none"}`)
	}
}

// ─────────────────────────────────────────────────────────
// Auto-brief
// ─────────────────────────────────────────────────────────

func (h *Handler) runAutoBrief(t *models.Task) {
	if err := h.db.UpdateBrief(t.ID, "", "pending"); err != nil {
		log.Printf("auto-brief: mark pending: %v", err)
		return
	}

	prompt := buildBriefPrompt(t)
	runner := &agent.ClaudeLocal{ClaudeBin: h.cfg().ClaudeBin}

	// Use a hidden session (name "[brief]") to run the agent
	sess := &models.Session{
		TaskID: t.ID,
		Name:   "[brief]",
		Mode:   models.SessionModeInteractive,
	}
	if err := h.db.CreateSession(sess); err != nil {
		log.Printf("auto-brief: create session: %v", err)
		h.db.UpdateBrief(t.ID, "Failed to create agent session", "error")
		return
	}

	ch, err := runner.Run(context.Background(), agent.RunOptions{Prompt: prompt})
	if err != nil {
		log.Printf("auto-brief: start agent: %v", err)
		h.db.UpdateBrief(t.ID, err.Error(), "error")
		h.db.UpdateSessionStatus(sess.ID, models.SessionStatusError, err.Error())
		return
	}
	h.db.UpdateSessionStatus(sess.ID, models.SessionStatusRunning, "")

	// Collect the last text event — that's the agent's final brief
	var lastText string
	for evt := range ch {
		switch evt.Kind {
		case agent.EventText:
			if evt.Content != "" {
				lastText = evt.Content
			}
		case agent.EventError:
			errMsg := "agent error"
			if evt.Err != nil {
				errMsg = evt.Err.Error()
			}
			log.Printf("auto-brief: agent error: %s", errMsg)
			h.db.UpdateBrief(t.ID, errMsg, "error")
			h.db.UpdateSessionStatus(sess.ID, models.SessionStatusError, errMsg)
			return
		}
	}

	if lastText == "" {
		lastText = "Agent completed but returned no content."
	}

	h.db.UpdateBrief(t.ID, lastText, "done")
	h.db.UpdateSessionStatus(sess.ID, models.SessionStatusComplete, "")
	log.Printf("auto-brief: done for task %s", t.ID)
}

func buildBriefPrompt(t *models.Task) string {
	var b strings.Builder
	b.WriteString("You are a senior software engineer acting as a preparation agent for a colleague's work task. ")
	b.WriteString("Your job is to investigate this task thoroughly and produce a concise, useful brief that helps your colleague hit the ground running.\n\n")

	b.WriteString("## Task details\n")
	b.WriteString("Title: " + t.Title + "\n")
	if t.Description != "" {
		b.WriteString("Description: " + t.Description + "\n")
	}
	b.WriteString("Type: " + t.WorkType + "\n")
	if t.PRURL != "" {
		b.WriteString("PR URL: " + t.PRURL + "\n")
	}
	if t.Link != "" {
		b.WriteString("Link: " + t.Link + "\n")
	}

	b.WriteString("\n## Your job\n")
	switch t.WorkType {
	case "pr_review":
		b.WriteString(`This is a pull request review task. Do the following:
1. Open the PR URL and read the description and diff thoroughly.
2. Understand what the PR is trying to achieve.
3. Identify any bugs, logic errors, edge cases, security concerns, or missing tests.
4. Note which files/areas deserve the most scrutiny.
5. Flag anything that feels off, unclear, or that warrants a comment.

Produce a structured brief with:
- **Summary**: what this PR does in 2-3 sentences
- **Key changes**: the most important files/functions changed and why they matter
- **Things to focus on**: specific areas that deserve careful review
- **Potential issues**: anything that looks wrong, risky, or incomplete — be specific
- **Questions to raise**: things worth discussing with the author

Be direct and specific. Skip pleasantries. Your output will be displayed directly to the reviewer.`)
	default:
		b.WriteString(`Investigate this task and produce a helpful brief. Consider:
- What is the goal and context?
- What systems, files, or people are likely involved?
- What are the key risks or unknowns?
- What would be most useful for someone picking this up to know?

Be concise and direct. Your output will be displayed to the person doing the task.`)
	}

	b.WriteString("\n\nWrite your final brief now. Do not add preamble like 'Here is my brief' — just the content.")
	return b.String()
}

// ─────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────

func (h *Handler) render(w http.ResponseWriter, name string, data interface{}) {
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "render error", 500)
	}
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func jsonStr(s string) string {
	// Simple JSON string encoding
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, c := range []byte(s) {
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, c)
		}
	}
	b = append(b, '"')
	return string(b)
}


