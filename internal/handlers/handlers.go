package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

type Handler struct {
	db        *db.DB
	cfg       *config.Config
	templates *template.Template
	ghToken   string
	claudeKey string
}

func New(d *db.DB, cfg *config.Config, tmplGlob string) (*Handler, error) {
	h := &Handler{
		db:        d,
		cfg:       cfg,
		ghToken:   os.Getenv("GITHUB_TOKEN"),
		claudeKey: os.Getenv("ANTHROPIC_API_KEY"),
	}

	funcMap := template.FuncMap{
		"workTypeDepth": cfg.WorkTypeDepth,
		"timeAgo":       timeAgo,
		"workTypeLabel": func(key string) string {
			if wt := cfg.WorkTypeByKey(key); wt != nil {
				return wt.Label
			}
			return key
		},
		"tierLabel": func(key string) string {
			if t := cfg.TierByKey(key); t != nil {
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
	mux.HandleFunc("DELETE /tasks/{id}", h.deleteTask)
	mux.HandleFunc("POST /tasks/{id}/analyse-pr", h.analysePR)
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.db.ListTasks(false, h.cfg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Group by tier key
	grouped := make(map[string][]*models.Task)
	for _, t := range tasks {
		grouped[t.Tier] = append(grouped[t.Tier], t)
	}

	// Build ordered tier list for the template
	tiers := make([]map[string]interface{}, len(h.cfg.Tiers))
	for i, tier := range h.cfg.Tiers {
		tiers[i] = map[string]interface{}{
			"Key":   tier.Key,
			"Label": tier.Label,
			"Tasks": grouped[tier.Key],
		}
	}

	h.render(w, "index.html", map[string]interface{}{
		"Tiers":     tiers,
		"WorkTypes": h.cfg.WorkTypes,
		"Config":    h.cfg,
	})
}

func (h *Handler) newTaskForm(w http.ResponseWriter, r *http.Request) {
	defaultTier := ""
	if len(h.cfg.Tiers) > 0 {
		defaultTier = h.cfg.Tiers[0].Key
	}
	h.render(w, "task_form.html", map[string]interface{}{
		"WorkTypes": h.cfg.WorkTypes,
		"Tiers":     h.cfg.Tiers,
		"Task":      &models.Task{Tier: defaultTier, Direction: "blocked_on_me"},
		"Edit":      false,
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) viewTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	h.render(w, "task_view.html", map[string]interface{}{
		"Task":   t,
		"Config": h.cfg,
	})
}

func (h *Handler) editTaskForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	h.render(w, "task_form.html", map[string]interface{}{
		"WorkTypes": h.cfg.WorkTypes,
		"Tiers":     h.cfg.Tiers,
		"Task":      t,
		"Edit":      true,
	})
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.db.GetTask(id)
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
	http.Redirect(w, r, "/tasks/"+id, http.StatusSeeOther)
}

func (h *Handler) markDone(w http.ResponseWriter, r *http.Request) {
	if err := h.db.MarkDone(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteTask(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) analysePR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if t.PRURL == "" {
		http.Error(w, "no PR URL on this task", 400)
		return
	}

	diff, err := h.fetchPRDiff(t.PRURL)
	if err != nil {
		http.Error(w, "failed to fetch PR diff: "+err.Error(), 500)
		return
	}

	summary, err := h.claudeSummarisePR(t.PRURL, diff)
	if err != nil {
		http.Error(w, "failed to analyse PR: "+err.Error(), 500)
		return
	}

	if err := h.db.UpdatePRSummary(id, summary); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="pr-summary-content">%s</div>`, template.HTMLEscapeString(summary))
}

func (h *Handler) fetchPRDiff(prURL string) (string, error) {
	// Parse https://github.com/owner/repo/pull/number
	prURL = strings.TrimRight(prURL, "/")
	parts := strings.Split(strings.TrimPrefix(prURL, "https://github.com/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", fmt.Errorf("invalid GitHub PR URL — expected https://github.com/owner/repo/pull/number")
	}
	owner, repo, number := parts[0], parts[1], parts[3]

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", owner, repo, number)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	req.Header.Set("User-Agent", "workflow-app")
	if h.ghToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.ghToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (h *Handler) claudeSummarisePR(prURL, diff string) (string, error) {
	if h.claudeKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	prompt := fmt.Sprintf(`You are a senior engineer helping review a pull request. Analyse this PR diff and provide a concise, structured review brief.

PR URL: %s

Format your response as:
## Summary
2-3 sentences on what this PR does.

## Key files to focus on
List the most important files/areas with a one-line note on why.

## Potential issues
Any bugs, logic errors, security concerns, or missing edge cases you spot. Be specific with line references where possible.

## Suggestions
Minor improvements, style notes, or things worth discussing.

---
DIFF:
%s`, prURL, diff)

	payload := map[string]interface{}{
		"model":      "claude-opus-4-5",
		"max_tokens": 1500,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", h.claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Claude API error: %s", result.Error.Message)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response from Claude")
	}
	return result.Content[0].Text, nil
}

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


