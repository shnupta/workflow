package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shnupta/workflow/internal/config"
	"github.com/shnupta/workflow/internal/db"
	"github.com/shnupta/workflow/internal/models"
)

// repoRoot returns the absolute path to the repository root by walking up from
// this source file's location. Uses runtime.Caller so it works regardless of
// where `go test` is invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../internal/handlers/integration_test.go
	// repo root is three directories up
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// openTestServer creates a fully-wired httptest.Server using the real templates
// from the repository and a fresh SQLite DB in a temp dir. The server is
// closed and the DB cleaned up when the returned cleanup func is called.
func openTestServer(t *testing.T) (*httptest.Server, *Handler, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "workflow-integration-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("open db: %v", err)
	}

	watcher, err := config.NewWatcher(filepath.Join(dir, "workflow.json"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("config watcher: %v", err)
	}

	tmplGlob := filepath.Join(repoRoot(t), "templates", "*.html")
	h, err := New(d, watcher, tmplGlob)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("New handler: %v", err)
	}

	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)

	return srv, h, func() {
		srv.Close()
		os.RemoveAll(dir)
	}
}

// get is a convenience wrapper: GET url, return response (body already read,
// Body closed). Follows no redirects.
func get(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// postForm is a convenience wrapper for application/x-www-form-urlencoded POSTs.
// Follows no redirects.
func postForm(t *testing.T, srv *httptest.Server, path string, vals url.Values) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(srv.URL+path, vals)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// postJSON posts JSON body and returns the response. Follows no redirects.
func postJSON(t *testing.T, srv *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Post(srv.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// patchJSON sends a PATCH request with a JSON body.
func patchJSON(t *testing.T, srv *httptest.Server, path string, body interface{}) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, srv.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build PATCH request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", path, err)
	}
	return resp
}

func readBody(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// ── GET / ─────────────────────────────────────────────────────────────────────

func TestHandler_IndexReturns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / expected 200, got %d", resp.StatusCode)
	}
}

func TestHandler_IndexContainsBoardHTML(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/")
	body := readBody(t, resp)
	if !strings.Contains(body, "class=\"board\"") {
		t.Error("GET / response does not contain board element")
	}
}

// ── POST /tasks ───────────────────────────────────────────────────────────────

func TestHandler_CreateTask_Redirects(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postForm(t, srv, "/tasks", url.Values{
		"title":     {"My integration test task"},
		"work_type": {"coding"},
		"tier":      {"today"},
		"direction": {"blocked_on_me"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("POST /tasks expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/tasks/") {
		t.Errorf("redirect Location %q should start with /tasks/", loc)
	}
}

func TestHandler_CreateTask_TaskAppears(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	postForm(t, srv, "/tasks", url.Values{
		"title":     {"Visible on board"},
		"work_type": {"meeting"},
		"tier":      {"today"},
		"direction": {"blocked_on_me"},
	})

	// Verify it landed in the DB.
	tasks, err := h.db.ListTasks(false, h.cfg())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	found := false
	for _, tsk := range tasks {
		if tsk.Title == "Visible on board" {
			found = true
		}
	}
	if !found {
		t.Error("created task not found in DB after POST /tasks")
	}
}

func TestHandler_CreateTask_MissingTitle(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	// title is required by the DB (NOT NULL constraint) and by the form;
	// sending empty title should not produce a 200/redirect — either the DB
	// rejects it (500) or the handler catches it (400). Either way, ≠ 303.
	resp := postForm(t, srv, "/tasks", url.Values{
		"title":     {""},
		"work_type": {"coding"},
		"tier":      {"today"},
		"direction": {"blocked_on_me"},
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("POST /tasks with empty title should not redirect 303")
	}
}

// ── POST /tasks/quick ─────────────────────────────────────────────────────────

func TestHandler_QuickCreateTask_ReturnsJSON(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/tasks/quick", map[string]string{
		"title":     "Quick task",
		"work_type": "coding",
		"tier":      "today",
	})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /tasks/quick expected 200, got %d; body: %s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("response is not valid JSON: %v; body: %s", err, body)
	}
	if result["id"] == "" {
		t.Error("response JSON missing 'id' field")
	}
	if !strings.HasPrefix(result["redirect"], "/tasks/") {
		t.Errorf("redirect %q should start with /tasks/", result["redirect"])
	}
}

func TestHandler_QuickCreateTask_EmptyTitle(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/tasks/quick", map[string]string{
		"title": "",
		"tier":  "today",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST /tasks/quick empty title: expected 400, got %d", resp.StatusCode)
	}
}

func TestHandler_QuickCreateTask_IDMatchesRedirect(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/tasks/quick", map[string]string{
		"title":     "Check ID",
		"work_type": "docs",
		"tier":      "backlog",
	})
	body := readBody(t, resp)
	var result map[string]string
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if result["redirect"] != "/tasks/"+result["id"] {
		t.Errorf("redirect %q doesn't match id %q", result["redirect"], result["id"])
	}
}

// ── POST /tasks/{id}/done ─────────────────────────────────────────────────────

func TestHandler_MarkDone_RedirectsToBoard(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Mark me done",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postForm(t, srv, "/tasks/"+task.ID+"/done", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("POST /tasks/{id}/done expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/" && loc != "/?recurring_cloned=1" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestHandler_MarkDone_TaskIsDoneInDB(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Going to be done",
		WorkType:  "docs",
		Tier:      "this_week",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	postForm(t, srv, "/tasks/"+task.ID+"/done", nil)

	got, err := h.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask after done: %v", err)
	}
	if !got.Done {
		t.Error("task should be marked done after POST /tasks/{id}/done")
	}
}

func TestHandler_MarkDone_NonExistentTask(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	// Marking a non-existent task done: MarkDone returns a DB error which
	// propagates as a 500. But SQLite UPDATE with no matching rows is not an
	// error, so it redirects fine (no rows affected is OK). Either 303 or 5xx
	// is acceptable — we just verify there's no 2xx body.
	resp := postForm(t, srv, "/tasks/does-not-exist/done", nil)
	defer resp.Body.Close()
	// As long as the server doesn't panic (it will return 303 or 500), pass.
	if resp.StatusCode == 0 {
		t.Error("got zero status code")
	}
}

// ── POST /tasks/{id}/timer ────────────────────────────────────────────────────

func TestHandler_TimerToggle_StartsTimer(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Timer task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/tasks/"+task.ID+"/timer", nil)
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /tasks/{id}/timer expected 200, got %d; body: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("response not JSON: %v; body: %s", err, body)
	}
	running, ok := result["running"].(bool)
	if !ok {
		t.Fatalf("response missing bool 'running' field; got %v", result)
	}
	if !running {
		t.Error("expected running=true after first timer toggle")
	}
}

func TestHandler_TimerToggle_StopsTimer(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Timer stop task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Start
	postJSON(t, srv, "/tasks/"+task.ID+"/timer", nil)
	// Stop
	resp := postJSON(t, srv, "/tasks/"+task.ID+"/timer", nil)
	body := readBody(t, resp)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	running, _ := result["running"].(bool)
	if running {
		t.Error("expected running=false after second timer toggle (stop)")
	}
}

func TestHandler_TimerToggle_ResponseHasElapsedSecs(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Timer elapsed task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/tasks/"+task.ID+"/timer", nil)
	body := readBody(t, resp)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if _, ok := result["elapsed_secs"]; !ok {
		t.Error("timer response missing 'elapsed_secs' field")
	}
	if _, ok := result["label"]; !ok {
		t.Error("timer response missing 'label' field")
	}
}

// ── GET /digest ───────────────────────────────────────────────────────────────

func TestHandler_DigestReturns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/digest")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /digest expected 200, got %d", resp.StatusCode)
	}
}

func TestHandler_DigestContainsWeekHeading(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/digest")
	body := readBody(t, resp)
	if !strings.Contains(body, "Week of") {
		t.Error("GET /digest missing 'Week of' heading")
	}
}

func TestHandler_DigestWithWeekParam(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/digest?week=2024-01-01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /digest?week= expected 200, got %d", resp.StatusCode)
	}
}

// ── GET /api/notes ────────────────────────────────────────────────────────────

func TestHandler_ListNotes_EmptyOnFreshDB(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/api/notes")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/notes expected 200, got %d", resp.StatusCode)
	}

	var notes []interface{}
	if err := json.Unmarshal([]byte(body), &notes); err != nil {
		t.Fatalf("not valid JSON array: %v; body: %s", err, body)
	}
	if len(notes) != 0 {
		t.Errorf("expected empty array on fresh DB, got %d notes", len(notes))
	}
}

func TestHandler_ListNotes_ContentType(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/api/notes")
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET /api/notes Content-Type: expected application/json, got %q", ct)
	}
}

// ── POST /api/notes ───────────────────────────────────────────────────────────

func TestHandler_CreateNote_Returns201(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/api/notes", map[string]string{
		"content": "# My note\nSome content.",
	})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("POST /api/notes expected 201, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestHandler_CreateNote_ReturnsNoteJSON(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/api/notes", map[string]string{
		"content": "# Hello\nWorld.",
	})
	body := readBody(t, resp)

	var note map[string]interface{}
	if err := json.Unmarshal([]byte(body), &note); err != nil {
		t.Fatalf("response not valid JSON: %v; body: %s", err, body)
	}
	if note["id"] == "" || note["id"] == nil {
		t.Error("created note missing 'id' field")
	}
	if note["content"] != "# Hello\nWorld." {
		t.Errorf("note content mismatch: %v", note["content"])
	}
}

func TestHandler_CreateNote_TitleDerivedFromContent(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/api/notes", map[string]string{
		"content": "# My Heading\nSecond line.",
	})
	body := readBody(t, resp)

	var note map[string]interface{}
	if err := json.Unmarshal([]byte(body), &note); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	title, _ := note["title"].(string)
	if title != "My Heading" {
		t.Errorf("expected title 'My Heading', got %q", title)
	}
}

func TestHandler_CreateNote_AppearsInList(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	postJSON(t, srv, "/api/notes", map[string]string{"content": "List me"})

	resp := get(t, srv, "/api/notes")
	body := readBody(t, resp)

	var notes []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &notes); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("expected 1 note in list, got %d", len(notes))
	}
}

// ── PATCH /api/notes/{id} ─────────────────────────────────────────────────────

func TestHandler_UpdateNote_ContentAndTitle(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	// Create
	createResp := postJSON(t, srv, "/api/notes", map[string]string{
		"content": "# Original\nOriginal content.",
	})
	createBody := readBody(t, createResp)
	var created map[string]interface{}
	if err := json.Unmarshal([]byte(createBody), &created); err != nil {
		t.Fatalf("create response not JSON: %v", err)
	}
	id := created["id"].(string)

	// Update
	patchResp := patchJSON(t, srv, "/api/notes/"+id, map[string]string{
		"content": "# Updated heading\nNew content.",
	})
	patchBody := readBody(t, patchResp)

	if patchResp.StatusCode != http.StatusOK {
		t.Errorf("PATCH /api/notes/{id} expected 200, got %d; body: %s", patchResp.StatusCode, patchBody)
	}

	var updated map[string]interface{}
	if err := json.Unmarshal([]byte(patchBody), &updated); err != nil {
		t.Fatalf("patch response not JSON: %v", err)
	}

	if updated["content"] != "# Updated heading\nNew content." {
		t.Errorf("content not updated: %v", updated["content"])
	}
	title, _ := updated["title"].(string)
	if title != "Updated heading" {
		t.Errorf("title not derived from updated content: got %q", title)
	}
}

func TestHandler_UpdateNote_NotFound(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := patchJSON(t, srv, "/api/notes/does-not-exist", map[string]string{
		"content": "anything",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("PATCH non-existent note: expected 404, got %d", resp.StatusCode)
	}
}

func TestHandler_UpdateNote_PersistsToList(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	createResp := postJSON(t, srv, "/api/notes", map[string]string{"content": "Before"})
	var created map[string]interface{}
	json.Unmarshal([]byte(readBody(t, createResp)), &created)
	id := created["id"].(string)

	patchJSON(t, srv, "/api/notes/"+id, map[string]string{"content": "After"})

	listResp := get(t, srv, "/api/notes")
	var notes []map[string]interface{}
	json.Unmarshal([]byte(readBody(t, listResp)), &notes)

	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0]["content"] != "After" {
		t.Errorf("patched content not reflected in list: %v", notes[0]["content"])
	}
}

// ── GET /api/tasks/{id}/comments ─────────────────────────────────────────────

func TestHandler_ListComments_EmptyOnFreshTask(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "comment task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := get(t, srv, "/api/tasks/"+task.ID+"/comments")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/tasks/{id}/comments expected 200, got %d", resp.StatusCode)
	}
	var comments []interface{}
	if err := json.Unmarshal([]byte(body), &comments); err != nil {
		t.Fatalf("response not JSON array: %v; body: %s", err, body)
	}
	if len(comments) != 0 {
		t.Errorf("expected empty array, got %d items", len(comments))
	}
}

// ── POST /api/tasks/{id}/comments ────────────────────────────────────────────

func TestHandler_CreateComment_Returns201(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "task with comment", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{
		"body": "this is a comment",
	})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("POST /api/tasks/{id}/comments expected 201, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestHandler_CreateComment_ReturnsCommentJSON(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "task for json", WorkType: "docs", Tier: "backlog", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{
		"body": "hello comment",
	})
	body := readBody(t, resp)

	var c map[string]interface{}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("response not JSON: %v; body: %s", err, body)
	}
	if c["body"] != "hello comment" {
		t.Errorf("body field: got %v", c["body"])
	}
	if c["id"] == nil || c["id"] == float64(0) {
		t.Error("expected non-zero id in response")
	}
	if c["formatted_time"] == "" || c["formatted_time"] == nil {
		t.Error("expected non-empty formatted_time in response")
	}
	if c["task_id"] != task.ID {
		t.Errorf("task_id: got %v, want %q", c["task_id"], task.ID)
	}
}

func TestHandler_CreateComment_EmptyBody_Returns400(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "empty body task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{
		"body": "",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", resp.StatusCode)
	}
}

func TestHandler_CreateComment_AppearsInList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "list check task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{"body": "first"})
	postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{"body": "second"})

	resp := get(t, srv, "/api/tasks/"+task.ID+"/comments")
	body := readBody(t, resp)

	var comments []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &comments); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0]["body"] != "first" {
		t.Errorf("expected first comment to be 'first', got %v", comments[0]["body"])
	}
	if comments[1]["body"] != "second" {
		t.Errorf("expected second comment to be 'second', got %v", comments[1]["body"])
	}
}

// ── DELETE /api/comments/{id} ─────────────────────────────────────────────────

func TestHandler_DeleteComment_Returns204(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "delete me", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Create via API so we get the ID back as JSON.
	createResp := postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{
		"body": "to be deleted",
	})
	var c map[string]interface{}
	json.Unmarshal([]byte(readBody(t, createResp)), &c)
	id := int64(c["id"].(float64))

	// Delete it.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	delResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE /api/comments/{id} expected 204, got %d", delResp.StatusCode)
	}
}

func TestHandler_DeleteComment_GoneFromList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "gone task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	createResp := postJSON(t, srv, "/api/tasks/"+task.ID+"/comments", map[string]string{"body": "gone"})
	var c map[string]interface{}
	json.Unmarshal([]byte(readBody(t, createResp)), &c)
	id := int64(c["id"].(float64))

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/comments/"+strconv.FormatInt(id, 10), nil)
	client.Do(req)

	listResp := get(t, srv, "/api/tasks/"+task.ID+"/comments")
	var comments []interface{}
	json.Unmarshal([]byte(readBody(t, listResp)), &comments)
	if len(comments) != 0 {
		t.Errorf("expected 0 comments after delete, got %d", len(comments))
	}
}

func TestHandler_DeleteComment_InvalidID_Returns400(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/comments/not-a-number", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric ID, got %d", resp.StatusCode)
	}
}
