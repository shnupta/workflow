package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shnupta/workflow/internal/models"
)

// TestAPIMarkDone verifies that POST /api/tasks/{id}/done marks the task done
// and returns JSON without redirecting.
func TestAPIMarkDone_MarksTaskDone(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{
		Title:     "In-progress task",
		WorkType:  "Coding",
		Tier:      "today",
		Direction: "on_me",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/done", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiMarkDone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]bool
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify in DB
	updated, err := h.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !updated.Done {
		t.Error("expected task.Done to be true after apiMarkDone")
	}
	if updated.DoneAt == nil {
		t.Error("expected DoneAt to be set")
	}
}

// TestAPIMarkDone_RecurringTaskCloned verifies that a recurring task produces
// cloned=true in the response.
func TestAPIMarkDone_RecurringTaskCloned(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{
		Title:      "Daily standup",
		WorkType:   "Meeting",
		Tier:       "today",
		Direction:  "on_me",
		Recurrence: "daily",
	}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/done", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiMarkDone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]bool
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result["cloned"] {
		t.Error("expected cloned=true for recurring task")
	}
}

// ── apiPatchTask ───────────────────────────────────────────────────────────

func TestAPIPatchTask_UpdatesTitle(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "original title", WorkType: "coding", Tier: "today", Direction: "on_me"}
	if err := h.db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	body := strings.NewReader(`{"title":"updated title"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID, body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiPatchTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	updated, _ := h.db.GetTask(task.ID)
	if updated.Title != "updated title" {
		t.Errorf("expected title 'updated title', got %q", updated.Title)
	}
}

func TestAPIPatchTask_EmptyTitleIgnored(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "keep me", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	body := strings.NewReader(`{"title":"   "}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID, body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiPatchTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	updated, _ := h.db.GetTask(task.ID)
	if updated.Title != "keep me" {
		t.Errorf("empty title should not overwrite existing, got %q", updated.Title)
	}
}

func TestAPIPatchTask_NotFound(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body := strings.NewReader(`{"title":"x"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/nonexistent", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "nonexistent")
	rr := httptest.NewRecorder()
	h.apiPatchTask(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// ── moveTask ───────────────────────────────────────────────────────────────

func TestMoveTask_ChangesTier(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "move me", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	body := strings.NewReader(`{"tier":"week"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/move", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.moveTask(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	updated, _ := h.db.GetTask(task.ID)
	if updated.Tier != "week" {
		t.Errorf("expected tier 'week', got %q", updated.Tier)
	}
}

// ── apiStarTask ────────────────────────────────────────────────────────────

func TestAPIStarTask_TogglesOn(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "star me", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID+"/star", nil)
	req.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiStarTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var result map[string]bool
	json.NewDecoder(rr.Body).Decode(&result)
	if !result["starred"] {
		t.Error("expected starred=true after first toggle")
	}
}

func TestAPIStarTask_TogglesOff(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	task := &models.Task{Title: "unstar me", WorkType: "coding", Tier: "today", Direction: "on_me"}
	h.db.CreateTask(task)

	// Star once
	req1 := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID+"/star", nil)
	req1.SetPathValue("id", task.ID)
	h.apiStarTask(httptest.NewRecorder(), req1)

	// Star again → should unstar
	req2 := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+task.ID+"/star", nil)
	req2.SetPathValue("id", task.ID)
	rr := httptest.NewRecorder()
	h.apiStarTask(rr, req2)

	var result map[string]bool
	json.NewDecoder(rr.Body).Decode(&result)
	if result["starred"] {
		t.Error("expected starred=false after second toggle")
	}
}

// ── quickCreateTask ────────────────────────────────────────────────────────

func TestQuickCreateTask_CreatesAndReturnsID(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body := strings.NewReader(`{"title":"Quick task","work_type":"coding","tier":"today"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/quick", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.quickCreateTask(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result map[string]string
	json.NewDecoder(rr.Body).Decode(&result)
	if result["id"] == "" {
		t.Error("expected non-empty id in response")
	}
	if result["redirect"] == "" {
		t.Error("expected redirect in response")
	}

	// Verify it's in DB
	task, err := h.db.GetTask(result["id"])
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Title != "Quick task" {
		t.Errorf("expected title 'Quick task', got %q", task.Title)
	}
}

func TestQuickCreateTask_EmptyTitleRejects(t *testing.T) {
	h, cleanup := openTestHandler(t)
	defer cleanup()

	body := strings.NewReader(`{"title":"  ","work_type":"coding","tier":"today"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/quick", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.quickCreateTask(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty title, got %d", rr.Code)
	}
}

// ── sanitize helpers ───────────────────────────────────────────────────────

func TestSanitizePriority_ValidValues(t *testing.T) {
	for _, v := range []string{"p1", "p2", "p3"} {
		if got := sanitizePriority(v); got != v {
			t.Errorf("sanitizePriority(%q): want %q, got %q", v, v, got)
		}
	}
}

func TestSanitizePriority_InvalidReturnsEmpty(t *testing.T) {
	for _, v := range []string{"", "P1", "high", "1"} {
		if got := sanitizePriority(v); got != "" {
			t.Errorf("sanitizePriority(%q): want empty, got %q", v, got)
		}
	}
}

func TestSanitizeEffort_ValidValues(t *testing.T) {
	for _, v := range []string{"xs", "s", "m", "l", "xl"} {
		if got := sanitizeEffort(v); got != v {
			t.Errorf("sanitizeEffort(%q): want %q, got %q", v, v, got)
		}
	}
}

func TestSanitizeEffort_InvalidReturnsEmpty(t *testing.T) {
	for _, v := range []string{"", "XL", "large", "medium"} {
		if got := sanitizeEffort(v); got != "" {
			t.Errorf("sanitizeEffort(%q): want empty, got %q", v, got)
		}
	}
}

func TestSanitizeRecurrence_ValidValues(t *testing.T) {
	for _, v := range []string{"daily", "weekly", "biweekly", "monthly"} {
		if got := sanitizeRecurrence(v); got != v {
			t.Errorf("sanitizeRecurrence(%q): want %q, got %q", v, v, got)
		}
	}
}

func TestSanitizeRecurrence_InvalidReturnsEmpty(t *testing.T) {
	for _, v := range []string{"", "Daily", "yearly", "once"} {
		if got := sanitizeRecurrence(v); got != "" {
			t.Errorf("sanitizeRecurrence(%q): want empty, got %q", v, got)
		}
	}
}

// ── parseDateForm ─────────────────────────────────────────────────────────

func TestParseDateForm_ValidDate(t *testing.T) {
	got := parseDateForm("2026-08-29")
	if got == nil {
		t.Fatal("expected non-nil for valid date")
	}
	if got.Year() != 2026 || got.Month() != 8 || got.Day() != 29 {
		t.Errorf("wrong date: %v", got)
	}
}

func TestParseDateForm_Empty(t *testing.T) {
	if parseDateForm("") != nil {
		t.Error("expected nil for empty string")
	}
}

func TestParseDateForm_Whitespace(t *testing.T) {
	if parseDateForm("   ") != nil {
		t.Error("expected nil for whitespace")
	}
}

func TestParseDateForm_Invalid(t *testing.T) {
	for _, v := range []string{"not-a-date", "29/08/2026", "2026-13-01"} {
		if parseDateForm(v) != nil {
			t.Errorf("expected nil for invalid %q", v)
		}
	}
}

// ── normaliseTags ──────────────────────────────────────────────────────────

func TestNormaliseTags_LowercasesAndTrims(t *testing.T) {
	got := normaliseTags("  Frontend , BACKEND , Api  ")
	// Should be lowercased, trimmed, joined
	for _, want := range []string{"frontend", "backend", "api"} {
		if !strings.Contains(got, want) {
			t.Errorf("normaliseTags: expected %q in %q", want, got)
		}
	}
}

func TestNormaliseTags_Deduplicates(t *testing.T) {
	got := normaliseTags("foo,foo,FOO,bar")
	// foo should appear only once
	count := strings.Count(got, "foo")
	if count != 1 {
		t.Errorf("expected foo to appear once, got %d in %q", count, got)
	}
}

func TestNormaliseTags_Empty(t *testing.T) {
	if got := normaliseTags(""); got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}

func TestNormaliseTags_SkipsBlanks(t *testing.T) {
	got := normaliseTags(",,,alpha,,,")
	if got != "alpha" {
		t.Errorf("expected 'alpha', got %q", got)
	}
}

// ── deriveTitleFromContent ─────────────────────────────────────────────────

func TestDeriveTitleFromContent_FirstLine(t *testing.T) {
	got := deriveTitleFromContent("Hello world\n\nSecond paragraph")
	if got != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", got)
	}
}

func TestDeriveTitleFromContent_StripsHashHeaders(t *testing.T) {
	got := deriveTitleFromContent("# My Heading\nsome content")
	if got != "My Heading" {
		t.Errorf("expected 'My Heading', got %q", got)
	}
}

func TestDeriveTitleFromContent_MultiHash(t *testing.T) {
	got := deriveTitleFromContent("## Section Title")
	if got != "Section Title" {
		t.Errorf("expected 'Section Title', got %q", got)
	}
}

func TestDeriveTitleFromContent_Empty(t *testing.T) {
	got := deriveTitleFromContent("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDeriveTitleFromContent_SkipsBlankFirstLine(t *testing.T) {
	got := deriveTitleFromContent("\n\nActual content\n")
	if got != "Actual content" {
		t.Errorf("expected 'Actual content', got %q", got)
	}
}

// ── jsonStr ────────────────────────────────────────────────────────────────

func TestJsonStr_PlainString(t *testing.T) {
	got := jsonStr("hello")
	if got != `"hello"` {
		t.Errorf("expected %q, got %q", `"hello"`, got)
	}
}

func TestJsonStr_EscapesQuotes(t *testing.T) {
	got := jsonStr(`say "hi"`)
	if !strings.Contains(got, `\"`) {
		t.Errorf("expected escaped quote in %q", got)
	}
}

func TestJsonStr_EscapesBackslash(t *testing.T) {
	got := jsonStr(`back\slash`)
	if !strings.Contains(got, `\\`) {
		t.Errorf("expected escaped backslash in %q", got)
	}
}

func TestJsonStr_Empty(t *testing.T) {
	got := jsonStr("")
	if got != `""` {
		t.Errorf("expected empty string literal, got %q", got)
	}
}

func TestSearchAll_EmptyQuery_Returns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()
	resp := get(t, srv, "/search")
	if resp.StatusCode != 200 {
		t.Errorf("GET /search → %d, want 200", resp.StatusCode)
	}
}

func TestSearchAll_WithQuery_Returns200(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()
	resp := get(t, srv, "/search?q=test")
	if resp.StatusCode != 200 {
		t.Errorf("GET /search?q=test → %d, want 200", resp.StatusCode)
	}
}

func TestSearchSessions_Routed(t *testing.T) {
	srv, _, cleanup := openTestServer(t)
	defer cleanup()
	resp := get(t, srv, "/search/sessions")
	if resp.StatusCode != 200 {
		t.Errorf("GET /search/sessions → %d, want 200", resp.StatusCode)
	}
}
