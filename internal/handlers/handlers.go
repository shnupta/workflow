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
	"sync"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/shnupta/workflow/internal/agent"
	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

// runnerRegistry tracks cancel funcs for active agent sessions so they can be
// interrupted on demand.
type runnerRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc // session ID → cancel
}

func newRunnerRegistry() *runnerRegistry {
	return &runnerRegistry{cancels: make(map[string]context.CancelFunc)}
}

func (r *runnerRegistry) register(sessionID string, cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancels[sessionID] = cancel
	r.mu.Unlock()
}

func (r *runnerRegistry) cancel(sessionID string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[sessionID]
	if ok {
		delete(r.cancels, sessionID)
	}
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func (r *runnerRegistry) deregister(sessionID string) {
	r.mu.Lock()
	delete(r.cancels, sessionID)
	r.mu.Unlock()
}

type Handler struct {
	db       *db.DB
	watcher  *config.Watcher
	tmplGlob string
	templates *template.Template
	registry  *runnerRegistry
}

func (h *Handler) cfg() *config.Config { return h.watcher.Get() }

func New(d *db.DB, watcher *config.Watcher, tmplGlob string) (*Handler, error) {
	h := &Handler{db: d, watcher: watcher, tmplGlob: tmplGlob, registry: newRunnerRegistry()}

	funcMap := template.FuncMap{
		"workTypeDepth": func(key string) string { return h.cfg().WorkTypeDepth(key) },
		"timeAgo":       timeAgo,
		"dueDateLabel":  dueDateLabel,
		"formatDate":    func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02")
		},
		"markdownHTML": func(s string) template.HTML {
			var buf bytes.Buffer
			md := goldmark.New(
				goldmark.WithExtensions(
					extension.GFM, // tables, strikethrough, task lists, linkify
					highlighting.NewHighlighting(
						highlighting.WithStyle("github-dark"),
						highlighting.WithFormatOptions(
							chromahtml.WithLineNumbers(false),
							chromahtml.WithClasses(false),
						),
					),
				),
			)
			if err := md.Convert([]byte(s), &buf); err != nil {
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
		"totalTimeLabel": func(secs int) string {
			if secs < 60 {
				return "< 1m"
			}
			h := secs / 3600
			m := (secs % 3600) / 60
			if h > 0 {
				return fmt.Sprintf("%dh %dm", h, m)
			}
			return fmt.Sprintf("%dm", m)
		},
		"lower":           strings.ToLower,
		"upper":           strings.ToUpper,
		"recurrenceLabel": recurrenceLabel,
		"jsonStr":         jsonStr,
		// jsonArr encodes a []string as a JSON array string safe for use in
		// HTML data-* attributes (e.g. data-tags='["a","b"]').
		"jsonArr": func(ss []string) string {
			if len(ss) == 0 {
				return "[]"
			}
			b, _ := json.Marshal(ss)
			return string(b)
		},
		"jsonEscape": func(s string) template.JS {
			b, _ := json.Marshal(s)
			return template.JS(b)
		},
		// tagOverflow returns len(tags)-n for use in "+N more" displays.
		"tagOverflow": func(tags []string, n int) int {
			if len(tags) <= n {
				return 0
			}
			return len(tags) - n
		},
		"priorityLabel": func(p string) string {
			switch p {
			case "p1":
				return "P1"
			case "p2":
				return "P2"
			case "p3":
				return "P3"
			default:
				return ""
			}
		},
		"effortLabel": func(e string) string {
			switch e {
			case "xs":
				return "XS"
			case "s":
				return "S"
			case "m":
				return "M"
			case "l":
				return "L"
			case "xl":
				return "XL"
			default:
				return ""
			}
		},
		"relTime": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			d := time.Since(*t)
			if d < time.Minute {
				return "just now"
			} else if d < time.Hour {
				return fmt.Sprintf("%dm ago", int(d.Minutes()))
			} else if d < 24*time.Hour {
				return fmt.Sprintf("%dh ago", int(d.Hours()))
			}
			return t.Format("Jan 2")
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
	mux.HandleFunc("POST /tasks/quick", h.quickCreateTask)
	mux.HandleFunc("GET /tasks/{id}", h.viewTask)
	mux.HandleFunc("GET /tasks/{id}/edit", h.editTaskForm)
	mux.HandleFunc("POST /tasks/{id}", h.updateTask)
	mux.HandleFunc("POST /tasks/{id}/done", h.markDone)
	mux.HandleFunc("POST /tasks/{id}/move", h.moveTask)
	mux.HandleFunc("DELETE /tasks/{id}", h.deleteTask)
	mux.HandleFunc("POST /tasks/{id}/duplicate", h.duplicateTask)
	mux.HandleFunc("POST /tasks/{id}/rebrief", h.rebrief)
	mux.HandleFunc("GET /tasks/{id}/brief-status", h.briefStatus)
	mux.HandleFunc("POST /tasks/{id}/timer", h.timerToggle)
	mux.HandleFunc("POST /tasks/{id}/timer/reset", h.timerReset)
	mux.HandleFunc("GET /tasks/{id}/timer", h.timerStatus)
	mux.HandleFunc("GET /tasks/{id}/brief-history", h.briefHistory)
	mux.HandleFunc("GET /api/tasks/{id}/briefs/diff", h.briefDiff)
	mux.HandleFunc("POST /tasks/{id}/brief/interrupt", h.interruptBrief)
	mux.HandleFunc("GET /api/tasks/{id}/scratchpad", h.apiScratchpad)
	mux.HandleFunc("PATCH /api/tasks/{id}/scratchpad", h.apiScratchpad)
	mux.HandleFunc("PATCH /api/tasks/{id}/star", h.apiStarTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", h.apiPatchTask)
	mux.HandleFunc("POST /api/tasks/{id}/blocked-by", h.apiSetBlockedBy)
	mux.HandleFunc("DELETE /api/tasks/{id}/blocked-by", h.apiClearBlockedBy)
	mux.HandleFunc("GET /api/tasks", h.apiSearchTasks)
	mux.HandleFunc("GET /api/tasks/recent", h.apiRecentTasks)
	mux.HandleFunc("GET /sessions", h.sessionsIndex)
	mux.HandleFunc("GET /search", h.searchSessions)
	mux.HandleFunc("GET /search/tasks", h.searchTasks)
	mux.HandleFunc("GET /search/notes", h.searchNotes)
	mux.HandleFunc("GET /notes", h.notesPage)
	mux.HandleFunc("GET /digest", h.weeklyDigest)
	h.registerSessionRoutes(mux)
	h.registerWebhookRoutes(mux)
	h.registerNoteRoutes(mux)
	h.registerTemplateRoutes(mux)
	h.registerCommentRoutes(mux)
	h.registerTagRoutes(mux)
	h.registerReminderRoutes(mux)
	h.registerExportRoutes(mux)
	h.registerCalendarRoutes(mux)
	h.registerStandupRoutes(mux)
}

func (h *Handler) sessionsIndex(w http.ResponseWriter, r *http.Request) {
	showArchived := r.URL.Query().Get("archived") == "1"
	sessions, err := h.db.ListAllSessions(showArchived)
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
		"Sessions":     sessions,
		"Groups":       groups,
		"ShowArchived": showArchived,
		"Nav":          "sessions",
	})
}

func (h *Handler) weeklyDigest(w http.ResponseWriter, r *http.Request) {
	// Determine which week to show. ?week=YYYY-MM-DD (any day in that week).
	// Default: current week (Monday).
	now := time.Now().UTC()
	weekParam := r.URL.Query().Get("week")
	var anchor time.Time
	if weekParam != "" {
		if t, err := time.Parse("2006-01-02", weekParam); err == nil {
			anchor = t
		} else {
			anchor = now
		}
	} else {
		anchor = now
	}
	// Find Monday of that week
	wd := int(anchor.Weekday())
	if wd == 0 {
		wd = 7 // Sunday → 7
	}
	monday := time.Date(anchor.Year(), anchor.Month(), anchor.Day()-wd+1, 0, 0, 0, 0, time.UTC)

	digest, err := h.db.WeeklyDigest(monday)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	prevWeek := monday.AddDate(0, 0, -7).Format("2006-01-02")
	nextWeek := monday.AddDate(0, 0, 7).Format("2006-01-02")
	isCurrentWeek := monday.Format("2006-01-02") == time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday()-1), 0, 0, 0, 0, time.UTC).Format("2006-01-02")

	h.render(w, "digest.html", map[string]interface{}{
		"Nav":           "digest",
		"Digest":        digest,
		"PrevWeek":      prevWeek,
		"NextWeek":      nextWeek,
		"IsCurrentWeek": isCurrentWeek,
	})
}

func (h *Handler) notesPage(w http.ResponseWriter, r *http.Request) {
	notes, _ := h.db.ListNotes("")
	h.render(w, "notes.html", map[string]interface{}{
		"Nav":   "notes",
		"Notes": notes,
	})
}

func (h *Handler) searchSessions(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var results []*db.SearchResult
	var searchErr string
	if q != "" {
		var err error
		results, err = h.db.SearchSessions(q)
		if err != nil {
			searchErr = err.Error()
		}
	}
	h.render(w, "search.html", map[string]interface{}{
		"Nav":       "sessions",
		"Query":     q,
		"Results":   results,
		"SearchErr": searchErr,
	})
}

func (h *Handler) searchTasks(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var results []*db.TaskSearchResult
	var searchErr string
	if q != "" {
		var err error
		results, err = h.db.SearchTasks(q)
		if err != nil {
			searchErr = err.Error()
		}
	}
	h.render(w, "task_search.html", map[string]interface{}{
		"Nav":       "search",
		"Query":     q,
		"Results":   results,
		"SearchErr": searchErr,
	})
}

func (h *Handler) searchNotes(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var results []*db.NoteSearchResult
	if q != "" {
		results, _ = h.db.SearchNotes(q)
	}
	h.render(w, "notes_search.html", map[string]interface{}{
		"Nav":     "search",
		"Query":   q,
		"Results": results,
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

	allTags, _ := h.db.ListAllTags() // nil on error → empty in template
	recentDone, _ := h.db.RecentlyDone(8) // last 8 tasks done in past 24h

	h.render(w, "index.html", map[string]interface{}{
		"Tiers":           tiers,
		"WorkTypes":       h.cfg().WorkTypes,
		"Nav":             "tasks",
		"RecurringCloned": r.URL.Query().Get("recurring_cloned") == "1",
		"AllTags":         allTags,
		"RecentDone":      recentDone,
	})
}

func (h *Handler) newTaskForm(w http.ResponseWriter, r *http.Request) {
	defaultTier := ""
	if len(h.cfg().Tiers) > 0 {
		defaultTier = h.cfg().Tiers[0].Key
	}
	// Pre-fill from template if redirected from POST /tasks/from-template/{id}.
	task := &models.Task{Tier: defaultTier, Direction: "blocked_on_me"}
	if r.URL.Query().Get("from_template") == "1" {
		task.WorkType = r.URL.Query().Get("work_type")
		task.Description = r.URL.Query().Get("description")
		task.Recurrence = sanitizeRecurrence(r.URL.Query().Get("recurrence"))
	}
	tmpls, _ := h.db.ListTemplates() // best-effort — page still works if this fails
	if tmpls == nil {
		tmpls = []*models.TaskTemplate{}
	}
	h.render(w, "task_form.html", map[string]interface{}{
		"WorkTypes":    h.cfg().WorkTypes,
		"Tiers":        h.cfg().Tiers,
		"Task":         task,
		"IsNew":        true,
		"Nav":          "tasks",
		"Templates":    tmpls,
		"FromTemplate": r.URL.Query().Get("tmpl_name"),
	})
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(r.FormValue("title")) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	t := &models.Task{
		Title:       strings.TrimSpace(r.FormValue("title")),
		Description: r.FormValue("description"),
		WorkType:    r.FormValue("work_type"),
		Tier:        r.FormValue("tier"),
		Direction:   r.FormValue("direction"),
		PRURL:       r.FormValue("pr_url"),
		Link:        r.FormValue("link"),
		DueDate:     parseDateForm(r.FormValue("due_date")),
		Recurrence:  sanitizeRecurrence(r.FormValue("recurrence")),
		Priority:    sanitizePriority(r.FormValue("priority")),
		Effort:      sanitizeEffort(r.FormValue("effort")),
	}
	if err := h.db.CreateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if shouldAutoBrief(t, r.FormValue("request_brief") == "1") {
		go h.runAutoBrief(t)
	}
	http.Redirect(w, r, "/tasks/"+t.ID, http.StatusSeeOther)
}

// quickCreateTask handles the inline "add task" form on the board.
// Accepts JSON: {"title","work_type","tier"} — minimal fields only.
// Returns JSON {"id","redirect"} so JS can optionally navigate to the task.
func (h *Handler) quickCreateTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    string `json:"title"`
		WorkType string `json:"work_type"`
		Tier     string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		http.Error(w, "title required", 400)
		return
	}
	if body.WorkType == "" {
		body.WorkType = "other"
	}
	if body.Tier == "" {
		if len(h.cfg().Tiers) > 0 {
			body.Tier = h.cfg().Tiers[0].Key
		}
	}
	t := &models.Task{
		Title:     body.Title,
		WorkType:  body.WorkType,
		Tier:      body.Tier,
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Quick-add doesn't have a brief checkbox — only auto-brief PR reviews
	if shouldAutoBrief(t, false) {
		go h.runAutoBrief(t)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":       t.ID,
		"redirect": "/tasks/" + t.ID,
	})
}

func (h *Handler) viewTask(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	sessions, _ := h.db.ListSessions(t.ID)
	briefVersions, _ := h.db.ListBriefVersions(t.ID)
	blocker, _ := h.db.GetBlockerTask(t.ID)
	comments, _ := h.db.ListComments(t.ID)
	reminders, _ := h.db.ListRemindersForTask(t.ID)

	// Split sessions into pinned / unpinned for prominent display.
	var pinnedSessions, unpinnedSessions []*models.Session
	for _, s := range sessions {
		if s.Pinned {
			pinnedSessions = append(pinnedSessions, s)
		} else {
			unpinnedSessions = append(unpinnedSessions, s)
		}
	}

	h.render(w, "task_view.html", map[string]interface{}{
		"Task":             t,
		"Sessions":         sessions,
		"PinnedSessions":   pinnedSessions,
		"UnpinnedSessions": unpinnedSessions,
		"BriefVersions":    briefVersions,
		"Blocker":          blocker,
		"Comments":         comments,
		"Reminders":        reminders,
		"Nav":           "tasks",
	})
}

// briefHistory returns all brief versions for a task as JSON.
func (h *Handler) interruptBrief(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if cancelled := h.registry.cancel(briefRegistryKey(taskID)); !cancelled {
		http.Error(w, "no brief in progress", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "interrupted"})
}

func (h *Handler) briefHistory(w http.ResponseWriter, r *http.Request) {
	versions, err := h.db.ListBriefVersions(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

// briefDiff returns a line-level diff between two brief versions.
// Query params: from=<version_id> (older) to=<version_id> (newer)
// If only one param is provided, diffs against the immediately adjacent version.
// Returns JSON: [{type: "add"|"del"|"eq", text: "..."}, ...]
func (h *Handler) briefDiff(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	fromID := r.URL.Query().Get("from")
	toID := r.URL.Query().Get("to")

	versions, err := h.db.ListBriefVersions(taskID)
	if err != nil || len(versions) < 2 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// Build a map for quick lookup.
	byID := make(map[string]*db.BriefVersion, len(versions))
	for _, v := range versions {
		byID[v.ID] = v
	}

	var older, newer *db.BriefVersion

	if fromID != "" && toID != "" {
		older = byID[fromID]
		newer = byID[toID]
	} else if toID != "" {
		// Find the version immediately after toID in the list (versions are newest-first).
		newer = byID[toID]
		for i, v := range versions {
			if v.ID == toID && i+1 < len(versions) {
				older = versions[i+1]
				break
			}
		}
	} else {
		// Default: diff the two most recent versions.
		newer = versions[0]
		older = versions[1]
	}

	if older == nil || newer == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	diff := computeLineDiff(older.Content, newer.Content)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(diff)
}

// DiffLine is one line in a computed diff.
type DiffLine struct {
	Type string `json:"type"` // "add", "del", "eq"
	Text string `json:"text"`
}

// computeLineDiff produces a simple line-level diff between two strings.
// Uses a basic LCS approach sufficient for brief text (not huge files).
func computeLineDiff(oldText, newText string) []DiffLine {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Build LCS table.
	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Trace back to build diff.
	var result []DiffLine
	i, j := 0, 0
	for i < m || j < n {
		if i < m && j < n && oldLines[i] == newLines[j] {
			result = append(result, DiffLine{Type: "eq", Text: oldLines[i]})
			i++
			j++
		} else if j < n && (i >= m || dp[i][j+1] >= dp[i+1][j]) {
			result = append(result, DiffLine{Type: "add", Text: newLines[j]})
			j++
		} else {
			result = append(result, DiffLine{Type: "del", Text: oldLines[i]})
			i++
		}
	}
	return result
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
		"Nav":       "tasks",
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
	t.DueDate = parseDateForm(r.FormValue("due_date"))
	t.Recurrence = sanitizeRecurrence(r.FormValue("recurrence"))
	t.Priority = sanitizePriority(r.FormValue("priority"))
	t.Effort = sanitizeEffort(r.FormValue("effort"))
	if err := h.db.UpdateTask(t); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tasks/"+t.ID, http.StatusSeeOther)
}

func (h *Handler) markDone(w http.ResponseWriter, r *http.Request) {
	cloned, err := h.db.MarkDone(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	target := "/"
	if cloned {
		target = "/?recurring_cloned=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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

// apiScratchpad handles GET and PATCH for a task's scratchpad field.
func (h *Handler) apiScratchpad(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		t, err := h.db.GetTask(id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content":%s}`, jsonStr(t.Scratchpad))
	case http.MethodPatch:
		t, err := h.db.GetTask(id)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		t.Scratchpad = body.Content
		if err := h.db.UpdateTask(t); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"content":%s}`, jsonStr(t.Scratchpad))
	default:
		http.Error(w, "method not allowed", 405)
	}
}

// apiSetBlockedBy sets the blocker for a task.
// apiStarTask handles PATCH /api/tasks/{id}/star — toggles the starred state.
// Returns JSON {"starred": true/false}.
func (h *Handler) apiStarTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	starred, err := h.db.StarTask(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"starred": starred})
}

// Body: {"blocked_by": "<task-id>"}
// Validates: target exists, is not done, is not the task itself.
func (h *Handler) apiSetBlockedBy(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var body struct {
		BlockedBy string `json:"blocked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.BlockedBy == "" {
		jsonError(w, "blocked_by is required", http.StatusBadRequest)
		return
	}
	if body.BlockedBy == taskID {
		jsonError(w, "a task cannot block itself", http.StatusBadRequest)
		return
	}
	blocker, err := h.db.GetTask(body.BlockedBy)
	if err != nil {
		jsonError(w, "blocker task not found", http.StatusNotFound)
		return
	}
	if blocker.Done {
		jsonError(w, "cannot block on a completed task", http.StatusBadRequest)
		return
	}
	if err := h.db.SetBlockedBy(taskID, body.BlockedBy); err != nil {
		jsonError(w, "failed to set blocker: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// apiClearBlockedBy removes the blocker from a task.
func (h *Handler) apiClearBlockedBy(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if err := h.db.ClearBlockedBy(taskID); err != nil {
		jsonError(w, "failed to clear blocker: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// apiRecentTasks handles GET /api/tasks/recent.
// Returns up to 8 most recently updated non-done tasks as JSON.
func (h *Handler) apiRecentTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.db.RecentTasks(8)
	if err != nil {
		jsonError(w, "recent tasks failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type taskResult struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Tier  string `json:"tier"`
	}
	out := make([]taskResult, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskResult{ID: t.ID, Title: t.Title, Tier: t.Tier})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// apiSearchTasks handles GET /api/tasks?q=QUERY.
// Returns up to 50 non-done tasks matching the query as JSON.
func (h *Handler) apiSearchTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	results, err := h.db.SearchTasks(q)
	if err != nil {
		jsonError(w, "search failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type taskResult struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Tier  string `json:"tier"`
		Done  bool   `json:"done"`
	}
	out := make([]taskResult, 0, len(results))
	for _, sr := range results {
		out = append(out, taskResult{
			ID:    sr.Task.ID,
			Title: sr.Task.Title,
			Tier:  sr.Task.Tier,
			Done:  sr.Task.Done,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// duplicateTask creates a copy of a task and redirects to it.
func (h *Handler) duplicateTask(w http.ResponseWriter, r *http.Request) {
	dup, err := h.db.DuplicateTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/tasks/"+dup.ID, http.StatusSeeOther)
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
// timerToggle starts/stops the timer for a task. Returns updated elapsed info as JSON.
func (h *Handler) timerToggle(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.TimerToggle(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	running := t.TimerStarted != nil
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"running":%v,"elapsed_secs":%d,"label":%s}`,
		running, t.ElapsedSeconds(), jsonStr(t.ElapsedLabel()))
}

// timerReset clears the timer.
func (h *Handler) timerReset(w http.ResponseWriter, r *http.Request) {
	if err := h.db.TimerReset(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"running":false,"elapsed_secs":0,"label":""}`)
}

// timerStatus returns current timer state as JSON (for polling).
func (h *Handler) timerStatus(w http.ResponseWriter, r *http.Request) {
	t, err := h.db.GetTask(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	running := t.TimerStarted != nil
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"running":%v,"elapsed_secs":%d,"label":%s}`,
		running, t.ElapsedSeconds(), jsonStr(t.ElapsedLabel()))
}

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

// shouldAutoBrief returns true if a brief should run automatically.
// PR Reviews always get a brief. Other types only if explicitly requested.
func shouldAutoBrief(t *models.Task, requested bool) bool {
	return t.WorkType == "pr_review" || requested
}

// briefRegistryKey returns the registry key used for a task's auto-brief cancel func.
func briefRegistryKey(taskID string) string { return "brief:" + taskID }

func (h *Handler) runAutoBrief(t *models.Task) {
	if err := h.db.UpdateBrief(t.ID, "", "pending"); err != nil {
		log.Printf("auto-brief: mark pending: %v", err)
		return
	}

	prompt := buildBriefPrompt(t)
	runner := agent.NewRunner(h.cfg())

	// Validate claude is reachable before even creating a session
	if err := runner.Validate(); err != nil {
		msg := "Claude CLI not found. Set `claude_bin` in workflow.json to the path of your Claude Code binary."
		log.Printf("auto-brief: claude not available: %v", err)
		h.db.UpdateBrief(t.ID, msg, "error")
		return
	}

	// Use a hidden session (name "[brief]") to run the agent
	sess := &models.Session{
		TaskID: t.ID,
		Name:   "[brief]",
		Mode:   models.SessionModeInteractive,
	}
	if err := h.db.CreateSession(sess); err != nil {
		log.Printf("auto-brief: create session: %v", err)
		h.db.UpdateBrief(t.ID, "Failed to create agent session.", "error")
		return
	}

	// Register a cancellable context so the brief can be interrupted
	ctx, cancel := context.WithCancel(context.Background())
	briefKey := briefRegistryKey(t.ID)
	h.registry.register(briefKey, cancel)
	defer h.registry.deregister(briefKey)

	ch, err := runner.Run(ctx, agent.RunOptions{Prompt: prompt})
	if err != nil {
		log.Printf("auto-brief: start agent: %v", err)
		h.db.UpdateBrief(t.ID, "Agent failed to start: "+err.Error(), "error")
		h.db.UpdateSessionStatus(sess.ID, models.SessionStatusError, err.Error())
		return
	}
	h.db.UpdateSessionStatus(sess.ID, models.SessionStatusRunning, "")

	// Collect the last text event — that's the agent's final brief
	var lastText string
	for evt := range ch {
		if ctx.Err() != nil {
			h.db.UpdateBrief(t.ID, "", "")
			h.db.UpdateSessionStatus(sess.ID, models.SessionStatusInterrupted, "interrupted by user")
			return
		}
		switch evt.Kind {
		case agent.EventText:
			if evt.Content != "" {
				lastText = evt.Content
			}
		case agent.EventError:
			if ctx.Err() != nil {
				h.db.UpdateBrief(t.ID, "", "")
				h.db.UpdateSessionStatus(sess.ID, models.SessionStatusInterrupted, "interrupted by user")
				return
			}
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

	if ctx.Err() != nil {
		h.db.UpdateBrief(t.ID, "", "")
		h.db.UpdateSessionStatus(sess.ID, models.SessionStatusInterrupted, "interrupted by user")
		return
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

	if t.Scratchpad != "" {
		b.WriteString("\n## Notes from the task owner\n")
		b.WriteString(t.Scratchpad)
		b.WriteString("\n")
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

// sanitizeRecurrence returns s if it's a known recurrence value, otherwise "".
func sanitizeRecurrence(s string) string {
	switch s {
	case "daily", "weekly", "biweekly", "monthly":
		return s
	default:
		return ""
	}
}

func sanitizePriority(s string) string {
	switch s {
	case "p1", "p2", "p3":
		return s
	default:
		return ""
	}
}

func sanitizeEffort(s string) string {
	switch s {
	case "xs", "s", "m", "l", "xl":
		return s
	default:
		return ""
	}
}

// parseDateForm parses a "2006-01-02" form value, returning nil if blank/invalid.
func parseDateForm(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// dueDateLabel returns a human-friendly due date string for display on cards.
// recurrenceLabel returns a human-friendly label for a recurrence value.
func recurrenceLabel(r string) string {
	switch r {
	case "daily":
		return "Daily"
	case "weekly":
		return "Weekly"
	case "biweekly":
		return "Every 2 weeks"
	case "monthly":
		return "Monthly"
	default:
		return ""
	}
}

func dueDateLabel(t *time.Time) string {
	if t == nil {
		return ""
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	due := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	diff := int(due.Sub(today).Hours() / 24)
	switch diff {
	case -1:
		return "Yesterday"
	case 0:
		return "Today"
	case 1:
		return "Tomorrow"
	default:
		if diff < 0 {
			return fmt.Sprintf("%dd overdue", -diff)
		}
		if diff <= 7 {
			return due.Weekday().String()[:3]
		}
		return due.Format("Jan 2")
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

// jsonError writes a JSON error response: {"error": "message"}.
func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}


// apiPatchTask handles PATCH /api/tasks/{id} for inline title/description editing.
func (h *Handler) apiPatchTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	t, err := h.db.GetTask(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if body.Title != nil {
		if trimmed := strings.TrimSpace(*body.Title); trimmed != "" {
			t.Title = trimmed
		}
	}
	if body.Description != nil {
		t.Description = *body.Description
	}
	if _, err := h.db.PatchTaskFields(id, t.Title, t.Description); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"title":%s,"description":%s}`, jsonStr(t.Title), jsonStr(t.Description))
}
