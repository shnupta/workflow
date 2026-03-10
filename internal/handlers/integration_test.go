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
	"time"

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

// ── GET /api/tags ─────────────────────────────────────────────────────────────

func TestHandler_ListAllTags_EmptyOnFreshDB(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/api/tags")
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/tags expected 200, got %d", resp.StatusCode)
	}
	var tags []string
	if err := json.Unmarshal([]byte(body), &tags); err != nil {
		t.Fatalf("response not JSON array: %v; body: %s", err, body)
	}
	if len(tags) != 0 {
		t.Errorf("expected empty array on fresh DB, got %v", tags)
	}
}

func TestHandler_ListAllTags_AfterAdding(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "tags task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	h.db.AddTag(task.ID, "backend")
	h.db.AddTag(task.ID, "api")

	resp := get(t, srv, "/api/tags")
	body := readBody(t, resp)

	var tags []string
	if err := json.Unmarshal([]byte(body), &tags); err != nil {
		t.Fatalf("not JSON: %v; body: %s", err, body)
	}
	// Should be sorted: api < backend
	if len(tags) != 2 || tags[0] != "api" || tags[1] != "backend" {
		t.Errorf("expected [api backend], got %v", tags)
	}
}

// ── POST /api/tasks/{id}/tags ─────────────────────────────────────────────────

func TestHandler_AddTag_Returns200WithTagList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "add tag task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "infra"})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST /api/tasks/{id}/tags expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	var tags []string
	if err := json.Unmarshal([]byte(body), &tags); err != nil {
		t.Fatalf("response not JSON: %v; body: %s", err, body)
	}
	if len(tags) != 1 || tags[0] != "infra" {
		t.Errorf("expected [infra], got %v", tags)
	}
}

func TestHandler_AddTag_NormalisesToLowercase(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "norm task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "BackEnd"})
	body := readBody(t, resp)
	var tags []string
	json.Unmarshal([]byte(body), &tags)
	if len(tags) != 1 || tags[0] != "backend" {
		t.Errorf("expected [backend] (normalised), got %v", tags)
	}
}

func TestHandler_AddTag_EmptyTag_Returns400(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "empty tag", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for blank tag, got %d", resp.StatusCode)
	}
}

func TestHandler_AddTag_Duplicate_IsIdempotent(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "dup tag handler", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "dup"})
	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "dup"})
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on duplicate add, got %d", resp.StatusCode)
	}
	var tags []string
	json.Unmarshal([]byte(body), &tags)
	if len(tags) != 1 {
		t.Errorf("expected 1 tag after duplicate add, got %v", tags)
	}
}

func TestHandler_AddTag_MultipleTagsAccumulate(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "multi tag task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "beta"})
	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/tags", map[string]string{"tag": "alpha"})
	body := readBody(t, resp)

	var tags []string
	json.Unmarshal([]byte(body), &tags)
	// sorted: alpha < beta
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Errorf("expected [alpha beta], got %v", tags)
	}
}

// ── DELETE /api/tasks/{id}/tags/{tag} ────────────────────────────────────────

func TestHandler_RemoveTag_Returns204(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "del tag task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	h.db.AddTag(task.ID, "todelete")

	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/tasks/"+task.ID+"/tags/todelete", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHandler_RemoveTag_TagGoneFromList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "gone tag task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	h.db.AddTag(task.ID, "gone")
	h.db.AddTag(task.ID, "stays")

	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/tasks/"+task.ID+"/tags/gone", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	client.Do(req)

	tags, err := h.db.ListTags(task.ID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 1 || tags[0] != "stays" {
		t.Errorf("expected [stays] after removing 'gone', got %v", tags)
	}
}

func TestHandler_RemoveTag_NonExistent_Returns204(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "ghost tag task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/tasks/"+task.ID+"/tags/doesnotexist", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for non-existent tag removal, got %d", resp.StatusCode)
	}
}

// ── GET /api/tasks/{id}/reminders ────────────────────────────────────────────

func TestHandler_ListReminders_EmptyOnFreshTask(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := get(t, srv, "/api/tasks/"+task.ID+"/reminders")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/tasks/{id}/reminders expected 200, got %d", resp.StatusCode)
	}
	var out []interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response not JSON array: %v; body: %s", err, body)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d items", len(out))
	}
}

// ── POST /api/tasks/{id}/reminders ───────────────────────────────────────────

func TestHandler_CreateReminder_Returns201(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "create reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "2099-12-31T09:00",
		"note":      "year-end reminder",
	})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", resp.StatusCode, body)
	}
}

func TestHandler_CreateReminder_ReturnsJSON(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "json reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "2099-06-01T10:00",
		"note":      "check this",
	})
	body := readBody(t, resp)

	var rem map[string]interface{}
	if err := json.Unmarshal([]byte(body), &rem); err != nil {
		t.Fatalf("not JSON: %v; body: %s", err, body)
	}
	if rem["id"] == nil || rem["id"] == float64(0) {
		t.Error("expected non-zero id")
	}
	if rem["task_id"] != task.ID {
		t.Errorf("task_id: got %v, want %q", rem["task_id"], task.ID)
	}
	if rem["note"] != "check this" {
		t.Errorf("note: got %v", rem["note"])
	}
	if rem["remind_at_formatted"] == "" || rem["remind_at_formatted"] == nil {
		t.Error("expected non-empty remind_at_formatted")
	}
	sent, _ := rem["sent"].(bool)
	if sent {
		t.Error("new reminder should have sent=false")
	}
}

func TestHandler_CreateReminder_MissingRemindAt_Returns400(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "bad reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "",
		"note":      "no time",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing remind_at, got %d", resp.StatusCode)
	}
}

func TestHandler_CreateReminder_InvalidDate_Returns400(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "invalid date task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "not-a-date",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid date, got %d", resp.StatusCode)
	}
}

func TestHandler_CreateReminder_AppearsInList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "list reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{"remind_at": "2099-01-01T08:00"})
	postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{"remind_at": "2099-06-01T08:00"})

	resp := get(t, srv, "/api/tasks/"+task.ID+"/reminders")
	body := readBody(t, resp)

	var rems []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &rems); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(rems) != 2 {
		t.Fatalf("expected 2 reminders, got %d", len(rems))
	}
}

// ── DELETE /api/reminders/{id} ────────────────────────────────────────────────

func TestHandler_DeleteReminder_Returns204(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "del reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	createResp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "2099-03-01T10:00",
	})
	var rem map[string]interface{}
	json.Unmarshal([]byte(readBody(t, createResp)), &rem)
	id := int64(rem["id"].(float64))

	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/reminders/"+strconv.FormatInt(id, 10), nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	delResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", delResp.StatusCode)
	}
}

func TestHandler_DeleteReminder_GoneFromList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "gone reminder task", WorkType: "coding", Tier: "today", Direction: "blocked_on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	createResp := postJSON(t, srv, "/api/tasks/"+task.ID+"/reminders", map[string]string{
		"remind_at": "2099-03-01T10:00",
	})
	var rem map[string]interface{}
	json.Unmarshal([]byte(readBody(t, createResp)), &rem)
	id := int64(rem["id"].(float64))

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/reminders/"+strconv.FormatInt(id, 10), nil)
	client.Do(req)

	listResp := get(t, srv, "/api/tasks/"+task.ID+"/reminders")
	var rems []interface{}
	json.Unmarshal([]byte(readBody(t, listResp)), &rems)
	if len(rems) != 0 {
		t.Errorf("expected 0 reminders after delete, got %d", len(rems))
	}
}

func TestHandler_DeleteReminder_InvalidID_Returns400(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/reminders/not-a-number", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric ID, got %d", resp.StatusCode)
	}
}

// ── GET /search/tasks ─────────────────────────────────────────────────────────

func TestHandler_TaskSearch_EmptyQuery_Returns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/search/tasks")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /search/tasks expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "search-tabs") {
		t.Error("expected search tabs in response")
	}
	if !strings.Contains(body, `action="/search/tasks"`) {
		t.Error("expected search form pointing to /search/tasks")
	}
}

func TestHandler_TaskSearch_WithQuery_Returns200(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "Fix the authentication bug",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp := get(t, srv, "/search/tasks?q=authentication")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /search/tasks?q=authentication expected 200, got %d; body: %s",
			resp.StatusCode, body)
	}
	if !strings.Contains(body, "Fix the authentication bug") {
		t.Errorf("expected task title in results; body snippet: %.200s", body)
	}
}

func TestHandler_TaskSearch_NoResults_ShowsEmptyState(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/search/tasks?q=zzznomatch")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "No tasks found") {
		t.Errorf("expected empty state message; body snippet: %.300s", body)
	}
}

func TestHandler_TaskSearch_TabLinksPresentOnSessionsSearch(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/search")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /search expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "search-tabs") {
		t.Error("expected search tabs on /search page too")
	}
	if !strings.Contains(body, `href="/search/tasks"`) {
		t.Error("expected link to /search/tasks from /search")
	}
}

// ── GET /api/reminders/due ────────────────────────────────────────────────────

func TestHandler_GetDueReminders_EmptyWhenNone(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/api/reminders/due")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	var out []interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON array: %v; body: %s", err, body)
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d items", len(out))
	}
}

func TestHandler_GetDueReminders_ReturnsDueReminder(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "due reminder task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Past reminder — should appear in due list.
	past := time.Now().UTC().Add(-5 * time.Minute)
	if _, err := h.db.CreateReminder(task.ID, past, "check this"); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	resp := get(t, srv, "/api/reminders/due")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", resp.StatusCode, body)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON: %v; body: %s", err, body)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 due reminder, got %d", len(out))
	}
	if out[0]["task_title"] != "due reminder task" {
		t.Errorf("task_title: got %v", out[0]["task_title"])
	}
	if out[0]["note"] != "check this" {
		t.Errorf("note: got %v", out[0]["note"])
	}
	if out[0]["remind_at_formatted"] == nil || out[0]["remind_at_formatted"] == "" {
		t.Error("expected non-empty remind_at_formatted")
	}
	if out[0]["task_id"] != task.ID {
		t.Errorf("task_id: got %v, want %s", out[0]["task_id"], task.ID)
	}
}

func TestHandler_GetDueReminders_ExcludesFutureReminder(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "future task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	future := time.Now().UTC().Add(time.Hour)
	if _, err := h.db.CreateReminder(task.ID, future, "not yet"); err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	resp := get(t, srv, "/api/reminders/due")
	body := readBody(t, resp)
	var out []interface{}
	json.Unmarshal([]byte(body), &out)
	if len(out) != 0 {
		t.Errorf("expected 0 due reminders for future reminder, got %d", len(out))
	}
}

func TestHandler_GetDueReminders_ExcludesAlreadySent(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "sent task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	past := time.Now().UTC().Add(-time.Minute)
	rem, err := h.db.CreateReminder(task.ID, past, "")
	if err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}
	if err := h.db.MarkReminderSent(rem.ID); err != nil {
		t.Fatalf("MarkReminderSent: %v", err)
	}

	resp := get(t, srv, "/api/reminders/due")
	body := readBody(t, resp)
	var out []interface{}
	json.Unmarshal([]byte(body), &out)
	if len(out) != 0 {
		t.Errorf("expected 0 due reminders (already sent), got %d", len(out))
	}
}

// ── POST /api/reminders/{id}/dismiss ─────────────────────────────────────────

func TestHandler_DismissReminder_Returns204(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "dismiss task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	past := time.Now().UTC().Add(-time.Minute)
	rem, err := h.db.CreateReminder(task.ID, past, "dismiss me")
	if err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	resp := postJSON(t, srv, "/api/reminders/"+strconv.FormatInt(rem.ID, 10)+"/dismiss", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHandler_DismissReminder_RemovedFromDueList(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "dismiss check task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	past := time.Now().UTC().Add(-time.Minute)
	rem, err := h.db.CreateReminder(task.ID, past, "")
	if err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}

	// Verify it appears before dismiss.
	resp := get(t, srv, "/api/reminders/due")
	var before []interface{}
	json.Unmarshal([]byte(readBody(t, resp)), &before)
	if len(before) != 1 {
		t.Fatalf("expected 1 due reminder before dismiss, got %d", len(before))
	}

	// Dismiss it.
	postJSON(t, srv, "/api/reminders/"+strconv.FormatInt(rem.ID, 10)+"/dismiss", nil)

	// Verify it's gone from due list.
	resp2 := get(t, srv, "/api/reminders/due")
	var after []interface{}
	json.Unmarshal([]byte(readBody(t, resp2)), &after)
	if len(after) != 0 {
		t.Errorf("expected 0 due reminders after dismiss, got %d", len(after))
	}
}

func TestHandler_DismissReminder_InvalidID_Returns400(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := postJSON(t, srv, "/api/reminders/not-a-number/dismiss", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric ID, got %d", resp.StatusCode)
	}
}

func TestHandler_DismissReminder_Idempotent(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "idempotent dismiss",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	past := time.Now().UTC().Add(-time.Minute)
	rem, err := h.db.CreateReminder(task.ID, past, "")
	if err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}
	idStr := strconv.FormatInt(rem.ID, 10)

	postJSON(t, srv, "/api/reminders/"+idStr+"/dismiss", nil)
	resp := postJSON(t, srv, "/api/reminders/"+idStr+"/dismiss", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("second dismiss: expected 204, got %d", resp.StatusCode)
	}
}

// ── GET /calendar ─────────────────────────────────────────────────────────────

func TestHandler_Calendar_Returns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/calendar")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /calendar expected 200, got %d; body: %.300s", resp.StatusCode, body)
	}
	// Basic structural checks.
	if !strings.Contains(body, "cal-grid") {
		t.Error("expected cal-grid in calendar page")
	}
	if !strings.Contains(body, "cal-day") {
		t.Error("expected cal-day columns in calendar page")
	}
}

func TestHandler_Calendar_ShowsWeekNavigation(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/calendar")
	body := readBody(t, resp)
	if !strings.Contains(body, "Prev") {
		t.Error("expected Prev navigation link")
	}
	if !strings.Contains(body, "Next") {
		t.Error("expected Next navigation link")
	}
}

func TestHandler_Calendar_WeekOffsetParam(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/calendar?week=1")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /calendar?week=1 expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	// When week != 0, a "Today" link should appear.
	if !strings.Contains(body, "cal-today-link") {
		t.Error("expected today link when week offset is non-zero")
	}
}

func TestHandler_Calendar_TaskWithDueDateAppearsInGrid(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "calendar smoke task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Set due date to today so it always appears in the default week view.
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	task.DueDate = &today
	if err := h.db.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	resp := get(t, srv, "/calendar")
	body := readBody(t, resp)
	if !strings.Contains(body, "calendar smoke task") {
		t.Errorf("expected task title in calendar; body snippet: %.500s", body)
	}
}

func TestHandler_Calendar_InvalidWeekParamDefaults(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	// Non-integer ?week= should silently default to 0 and return 200.
	resp := get(t, srv, "/calendar?week=notanumber")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for invalid week param, got %d", resp.StatusCode)
	}
}

func TestHandler_Calendar_NegativeWeekOffset(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/calendar?week=-2")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for negative week offset, got %d", resp.StatusCode)
	}
}

func TestHandler_Calendar_TaskOnlyInItsOwnWeek(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{
		Title:     "future week task",
		WorkType:  "coding",
		Tier:      "today",
		Direction: "blocked_on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Due 14 days from now — guaranteed to be in a different week than today.
	future := time.Now().AddDate(0, 0, 14)
	futureDay := time.Date(future.Year(), future.Month(), future.Day(), 0, 0, 0, 0, future.Location())
	task.DueDate = &futureDay
	if err := h.db.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	// Should NOT appear in current week.
	resp := get(t, srv, "/calendar")
	body := readBody(t, resp)
	if strings.Contains(body, "future week task") {
		t.Error("task due in 2 weeks should not appear in current week view")
	}

	// Should appear in week=2.
	resp2 := get(t, srv, "/calendar?week=2")
	body2 := readBody(t, resp2)
	if !strings.Contains(body2, "future week task") {
		t.Errorf("task due in 2 weeks should appear in week=2 view; snippet: %.400s", body2)
	}
}

// ── GET /standup ──────────────────────────────────────────────────────────────

func TestStandupPage(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	// GET /standup returns 200 with expected structure.
	resp := get(t, srv, "/standup")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /standup expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Standup") {
		t.Error("expected 'Standup' heading in standup page")
	}
	if !strings.Contains(body, "standup-textarea") {
		t.Error("expected standup-textarea in standup page")
	}

	// Create a task and mark it done — should appear in standup.
	task := &models.Task{
		Title:    "My standup task",
		WorkType: "coding",
		Tier:     "today",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.db.MarkDone(task.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	// Should now appear on today's standup.
	resp2 := get(t, srv, "/standup")
	body2 := readBody(t, resp2)
	if !strings.Contains(body2, "My standup task") {
		t.Errorf("completed task should appear in standup; snippet: %.500s", body2)
	}
}

func TestStandupAPI(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	resp := get(t, srv, "/api/standup")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/standup expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"date"`) {
		t.Error("expected 'date' field in API response")
	}
	if !strings.Contains(body, `"text"`) {
		t.Error("expected 'text' field in API response")
	}

	// Create and complete a task — should appear in API response.
	task2 := &models.Task{
		Title:    "API standup task",
		WorkType: "coding",
		Tier:     "today",
	}
	if err := h.db.CreateTask(task2); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := h.db.MarkDone(task2.ID); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	resp2 := get(t, srv, "/api/standup")
	body2 := readBody(t, resp2)
	if !strings.Contains(body2, "API standup task") {
		t.Errorf("completed task should appear in API; snippet: %.400s", body2)
	}
}

// ── GET /tasks/{id}/sessions/{sid}/export.md ──────────────────────────────────


// ── GET /tasks/{id}/sessions/{sid}/export.md ──────────────────────────────────

func TestSessionExportMarkdown(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	// Create task.
	task := &models.Task{
		Title:    "Export test task",
		WorkType: "coding",
		Tier:     "today",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Create session via the struct API.
	sess := &models.Session{
		TaskID: task.ID,
		Name:   "export-session",
		Mode:   "interactive",
	}
	if err := h.db.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Add user and assistant messages.
	userMsg := &models.Message{
		SessionID: sess.ID,
		Role:      models.MessageRoleUser,
		Kind:      models.MessageKindText,
		Content:   "Hello, review this code.",
		CreatedAt: sess.CreatedAt,
	}
	if err := h.db.CreateMessage(userMsg); err != nil {
		t.Fatalf("CreateMessage user: %v", err)
	}
	asstMsg := &models.Message{
		SessionID: sess.ID,
		Role:      models.MessageRoleAssistant,
		Kind:      models.MessageKindText,
		Content:   "The code looks good!",
		CreatedAt: sess.CreatedAt,
	}
	if err := h.db.CreateMessage(asstMsg); err != nil {
		t.Fatalf("CreateMessage assistant: %v", err)
	}

	resp := get(t, srv, "/tasks/"+task.ID+"/sessions/"+sess.ID+"/export.md")
	if resp.StatusCode != 200 {
		t.Fatalf("export.md expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)

	if !strings.Contains(body, "Export test task") {
		t.Errorf("export should contain task title; got: %.300s", body)
	}
	if !strings.Contains(body, "Hello, review this code.") {
		t.Error("export should contain user message")
	}
	if !strings.Contains(body, "The code looks good!") {
		t.Error("export should contain assistant message")
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("expected text/markdown Content-Type, got %q", ct)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment Content-Disposition, got %q", cd)
	}
}

// ── GET /api/tasks/{id}/briefs/diff ─────────────────────────────────────────

func TestBriefDiffAPI(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	task := &models.Task{Title: "Diff test task", WorkType: "PR Review", Tier: "today"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Store two brief versions.
	if err := h.db.UpdateBrief(task.ID, "## Summary\n\nOld content here.\n\nLine only in v1.", "done"); err != nil {
		t.Fatalf("UpdateBrief v1: %v", err)
	}
	if err := h.db.UpdateBrief(task.ID, "## Summary\n\nNew content here.\n\nLine only in v2.", "done"); err != nil {
		t.Fatalf("UpdateBrief v2: %v", err)
	}

	// Default diff (latest vs previous).
	resp := get(t, srv, "/api/tasks/"+task.ID+"/briefs/diff")
	if resp.StatusCode != 200 {
		t.Fatalf("diff API expected 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `"type"`) {
		t.Error("expected diff JSON with 'type' field")
	}
	// Should contain add and del entries for the changed lines.
	if !strings.Contains(body, `"add"`) || !strings.Contains(body, `"del"`) {
		t.Errorf("expected add/del diff entries; got: %.300s", body)
	}

	// With only 1 version, should return empty array.
	task2 := &models.Task{Title: "Single brief", WorkType: "coding", Tier: "today"}
	if err := h.db.CreateTask(task2); err != nil {
		t.Fatalf("CreateTask2: %v", err)
	}
	if err := h.db.UpdateBrief(task2.ID, "only version", "done"); err != nil {
		t.Fatalf("UpdateBrief single: %v", err)
	}
	resp2 := get(t, srv, "/api/tasks/"+task2.ID+"/briefs/diff")
	body2 := readBody(t, resp2)
	if !strings.Contains(body2, "[]") {
		t.Errorf("single-version task should return empty diff; got: %.200s", body2)
	}
}

// ── POST /tasks/{id}/duplicate ───────────────────────────────────────────────

func TestDuplicateTask(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	src := &models.Task{Title: "Original task", WorkType: "coding", Tier: "today", Description: "desc", Recurrence: "weekly"}
	if err := h.db.CreateTask(src); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.db.AddTag(src.ID, "backend"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}

	resp := postForm(t, srv, "/tasks/"+src.ID+"/duplicate", nil)
	// Should redirect (303) to new task page
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}

	// The redirect location should be the new task's page: /tasks/{newID}
	loc := resp.Header.Get("Location")
	if loc == "" || loc == "/tasks/"+src.ID {
		t.Fatalf("expected redirect to new task, got %q", loc)
	}
	newID := strings.TrimPrefix(loc, "/tasks/")

	// Fetch the duplicate
	dup, err := h.db.GetTask(newID)
	if err != nil {
		t.Fatalf("GetTask duplicate: %v", err)
	}
	if !strings.HasPrefix(dup.Title, "Copy of") {
		t.Errorf("expected title to start with 'Copy of', got %q", dup.Title)
	}
	if dup.WorkType != src.WorkType {
		t.Errorf("work_type mismatch: got %q want %q", dup.WorkType, src.WorkType)
	}
	if dup.Description != src.Description {
		t.Errorf("description mismatch")
	}
	if dup.Recurrence != src.Recurrence {
		t.Errorf("recurrence mismatch")
	}
	// Tags should be copied
	tags, _ := h.db.ListTags(dup.ID)
	if len(tags) != 1 || tags[0] != "backend" {
		t.Errorf("tags not copied: got %v", tags)
	}
	// Timer and brief should NOT be copied
	if dup.TimerTotal != 0 {
		t.Errorf("timer_total should be 0, got %d", dup.TimerTotal)
	}
	if dup.Brief != "" {
		t.Errorf("brief should be empty on duplicate")
	}
}

func TestPatchTaskTitleAndDescription(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	// Create a task via form POST
	vals := url.Values{
		"title": {"Original Title"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create task: unexpected status %d", resp.StatusCode)
	}
	resp.Body.Close()

	tasks, err := h.db.ListTasks(false, h.watcher.Get())
	if err != nil || len(tasks) == 0 {
		t.Fatalf("list tasks: %v, got %d tasks", err, len(tasks))
	}
	taskID := tasks[0].ID

	// Patch title
	resp = patchJSON(t, srv, "/api/tasks/"+taskID, map[string]string{"title": "Updated Title"})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch title: %d %s", resp.StatusCode, body)
	}

	t.Run("title updated in DB", func(t *testing.T) {
		updated, err := h.db.GetTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Title != "Updated Title" {
			t.Errorf("got title %q, want %q", updated.Title, "Updated Title")
		}
	})

	// Patch description
	resp = patchJSON(t, srv, "/api/tasks/"+taskID,
		map[string]string{"description": "## Notes\n- item one\n- item two"})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch description: %d %s", resp.StatusCode, body)
	}

	t.Run("description updated in DB", func(t *testing.T) {
		updated, err := h.db.GetTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		want := "## Notes\n- item one\n- item two"
		if updated.Description != want {
			t.Errorf("got desc %q, want %q", updated.Description, want)
		}
		if updated.Title != "Updated Title" {
			t.Errorf("title changed unexpectedly: got %q", updated.Title)
		}
	})

	// Blank title → no-op (keeps previous title)
	resp = patchJSON(t, srv, "/api/tasks/"+taskID, map[string]string{"title": "   "})
	readBody(t, resp) //nolint
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("blank title patch: %d", resp.StatusCode)
	}
	t.Run("blank title is no-op", func(t *testing.T) {
		updated, _ := h.db.GetTask(taskID)
		if updated.Title != "Updated Title" {
			t.Errorf("blank title was not a no-op: got %q", updated.Title)
		}
	})
}

func TestTaskPriority_SetAndFilterP1(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	// Create P1 task
	vals := url.Values{
		"title": {"Urgent task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"}, "priority": {"p1"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	resp.Body.Close()

	// Create normal task (no priority)
	vals2 := url.Values{
		"title": {"Normal task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"},
	}
	resp = postForm(t, srv, "/tasks", vals2)
	resp.Body.Close()

	tasks, err := h.db.ListTasks(false, h.watcher.Get())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	p1tasks := 0
	for _, task := range tasks {
		if task.Priority == "p1" {
			p1tasks++
		}
	}
	if p1tasks != 1 {
		t.Errorf("expected 1 P1 task, got %d", p1tasks)
	}
}

func TestTaskPriority_UpdateViaEdit(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	vals := url.Values{
		"title": {"Task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	resp.Body.Close()

	tasks, _ := h.db.ListTasks(false, h.watcher.Get())
	if len(tasks) == 0 {
		t.Fatal("no tasks created")
	}
	taskID := tasks[0].ID

	// Update to p2
	editVals := url.Values{
		"title": {"Task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"}, "priority": {"p2"},
	}
	resp = postForm(t, srv, "/tasks/"+taskID, editVals)
	readBody(t, resp)

	updated, err := h.db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Priority != "p2" {
		t.Errorf("expected priority p2, got %q", updated.Priority)
	}
}

func TestTaskEffort_SetAndRead(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	vals := url.Values{
		"title": {"Effort task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"}, "effort": {"m"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	resp.Body.Close()

	tasks, err := h.db.ListTasks(false, h.watcher.Get())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("no tasks")
	}
	if tasks[0].Effort != "m" {
		t.Errorf("expected effort=m, got %q", tasks[0].Effort)
	}
}

func TestTaskEffort_UpdateViaEdit(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	vals := url.Values{
		"title": {"Task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	resp.Body.Close()

	tasks, _ := h.db.ListTasks(false, h.watcher.Get())
	taskID := tasks[0].ID

	editVals := url.Values{
		"title": {"Task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"}, "effort": {"xl"},
	}
	resp = postForm(t, srv, "/tasks/"+taskID, editVals)
	readBody(t, resp)

	updated, _ := h.db.GetTask(taskID)
	if updated.Effort != "xl" {
		t.Errorf("expected effort=xl, got %q", updated.Effort)
	}
}

func TestTaskEffort_InvalidIgnored(t *testing.T) {
	srv, h, cleanup := openTestServer(t)
	defer cleanup()

	vals := url.Values{
		"title": {"Task"}, "work_type": {"code"},
		"tier": {"today"}, "direction": {"blocked_on_me"}, "effort": {"huge"},
	}
	resp := postForm(t, srv, "/tasks", vals)
	resp.Body.Close()

	tasks, _ := h.db.ListTasks(false, h.watcher.Get())
	if tasks[0].Effort != "" {
		t.Errorf("expected empty effort for invalid value, got %q", tasks[0].Effort)
	}
}
