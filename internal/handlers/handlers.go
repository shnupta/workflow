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

	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

type Handler struct {
	db        *db.DB
	templates *template.Template
	ghToken   string
	claudeKey string
}

func New(d *db.DB, tmplGlob string) (*Handler, error) {
	funcMap := template.FuncMap{
		"workTypeDepth": models.WorkTypeDepth,
		"upper":         strings.ToUpper,
		"timeAgo":       timeAgo,
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseGlob(tmplGlob)
	if err != nil {
		return nil, err
	}
	return &Handler{
		db:        d,
		templates: tmpl,
		ghToken:   os.Getenv("GITHUB_TOKEN"),
		claudeKey: os.Getenv("ANTHROPIC_API_KEY"),
	}, nil
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
	tasks, err := h.db.ListTasks(false)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Group by tier
	grouped := map[models.Tier][]*models.Task{
		models.TierToday:    {},
		models.TierThisWeek: {},
		models.TierBacklog:  {},
	}
	for _, t := range tasks {
		grouped[t.Tier] = append(grouped[t.Tier], t)
	}

	data := map[string]interface{}{
		"Today":     grouped[models.TierToday],
		"ThisWeek":  grouped[models.TierThisWeek],
		"Backlog":   grouped[models.TierBacklog],
		"AllTasks":  tasks,
		"WorkTypes": models.AllWorkTypes(),
		"Tiers":     models.AllTiers(),
	}
	h.render(w, "index.html", data)
}

func (h *Handler) newTaskForm(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"WorkTypes": models.AllWorkTypes(),
		"Tiers":     models.AllTiers(),
		"Task":      &models.Task{Tier: models.TierToday, Direction: models.DirectionBlockedOnMe},
	}
	h.render(w, "task_form.html", data)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t := &models.Task{
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		WorkType:    models.WorkType(r.FormValue("work_type")),
		Tier:        models.Tier(r.FormValue("tier")),
		Direction:   models.Direction(r.FormValue("direction")),
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
	h.render(w, "task_view.html", map[string]interface{}{"Task": t})
}

func (h *Handler) editTaskForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := h.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	data := map[string]interface{}{
		"WorkTypes": models.AllWorkTypes(),
		"Tiers":     models.AllTiers(),
		"Task":      t,
		"Edit":      true,
	}
	h.render(w, "task_form.html", data)
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
	t.WorkType = models.WorkType(r.FormValue("work_type"))
	t.Tier = models.Tier(r.FormValue("tier"))
	t.Direction = models.Direction(r.FormValue("direction"))
	t.PRURL = r.FormValue("pr_url")
	t.Link = r.FormValue("link")
	if err := h.db.UpdateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tasks/"+id, http.StatusSeeOther)
}

func (h *Handler) markDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.db.MarkDone(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.db.DeleteTask(id); err != nil {
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
		http.Error(w, "no PR URL", 400)
		return
	}

	diff, err := h.fetchPRDiff(t.PRURL)
	if err != nil {
		http.Error(w, "failed to fetch PR: "+err.Error(), 500)
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

	// Return just the summary fragment for HTMX
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="pr-summary-content">%s</div>`, template.HTMLEscapeString(summary))
}

// fetchPRDiff fetches the PR diff from GitHub API
func (h *Handler) fetchPRDiff(prURL string) (string, error) {
	// Parse github.com/owner/repo/pull/number
	prURL = strings.TrimRight(prURL, "/")
	parts := strings.Split(strings.TrimPrefix(prURL, "https://github.com/"), "/")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid GitHub PR URL: %s", prURL)
	}
	owner, repo, number := parts[0], parts[1], parts[3]

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%s", owner, repo, number)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	req.Header.Set("User-Agent", "flow-app")
	if h.ghToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.ghToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024)) // cap at 500KB
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// claudeSummarisePR sends the diff to Claude and returns a structured summary
func (h *Handler) claudeSummarisePR(prURL, diff string) (string, error) {
	if h.claudeKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	// Trim diff if very large
	if len(diff) > 80000 {
		diff = diff[:80000] + "\n\n[diff truncated]"
	}

	prompt := fmt.Sprintf(`You are a senior engineer helping review a pull request. Analyse this PR diff and provide a concise, structured review brief.

PR URL: %s

Format your response as:
## Summary
2-3 sentences on what this PR does.

## Key files to focus on
List the most important files/areas with a one-line note on why.

## Potential issues
Any bugs, logic errors, security concerns, or missing edge cases you spot. Be specific with line references if possible.

## Suggestions
Minor improvements, style notes, or things worth discussing.

---
DIFF:
%s`, prURL, diff)

	payload := map[string]interface{}{
		"model":      "claude-opus-4-5",
		"max_tokens": 1024,
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
