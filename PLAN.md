# workflow — Build Plan

This is the canonical task tracker for the workflow app.
Nox reads and updates this file when working on the project overnight or between sessions.

## How Nox works on this repo

- Repo lives at `/root/.nox/workspace/workflow/` on the server
- Go is at `/usr/local/go/bin/go` — always use `export PATH=$PATH:/usr/local/go/bin` before running go commands
- Build check: `cd /root/.nox/workspace/workflow && make build` (or `go build -tags fts5 ./...`)
- After each change: `make build` to verify, then `git add -A && git commit -m "..." && git push`
- Casey pulls and restarts the binary on his machine to pick up changes
- **Nox CAN run the server on the server for testing** — run it in the background, test with Playwright, then kill it
- Start test server:
  ```bash
  export PATH=$PATH:/usr/local/go/bin
  cd /root/.nox/workspace/workflow
  make build BINARY=/tmp/workflow-test  # or: go build -tags fts5 -o /tmp/workflow-test ./cmd/workflow/
  mkdir -p /tmp/wf-test-data
  /tmp/workflow-test serve -addr :7071 -dir /tmp/wf-test-data -templates "$(pwd)/templates/*.html" > /tmp/workflow-test.log 2>&1 &
  echo "started PID $!"
  ```
- Test with Playwright MCP (`mcp_call` with `mcp: "playwright"`) — navigate to `http://localhost:7071`, screenshot, interact
- Kill when done: `kill $(lsof -t -i:7071) 2>/dev/null && rm -f /tmp/workflow-test`
- After testing, always `git add -A && git commit && git push` — Casey pulls + restarts his binary
- Claude binary on Casey's machine: `/root/.local/bin/claude` (confirmed working 2026-03-05)

## Architecture

```
cmd/workflow/main.go         — entrypoint; subcommands: setup, serve, start, stop, restart, status, update
internal/config/config.go    — Config struct, hot-reload watcher (2s poll)
internal/db/db.go            — SQLite, migrations, FTS5 index, all DB methods
internal/models/             — Task, Session, Message structs with json+db tags
internal/handlers/
  handlers.go                — task CRUD, auto-brief agent, search, render helpers
  sessions.go                — session CRUD, chat endpoints, message queue
internal/agent/
  agent.go                   — Runner interface, Event types (provider-agnostic)
  claude_local.go            — Claude CLI runner (stream-json, stderr capture)
  runner.go                  — RunSession: drives agent, writes normalised messages to DB
internal/daemon/daemon.go    — launchd plist management (macOS service: start/stop/status)
internal/setup/setup.go      — interactive setup wizard (claude_bin detection, data dir, service install)
templates/                   — Go html/template files (nav.html, search.html, sessions_index.html, …)
static/css/style.css         — All styles (dark theme, CSS variables)
workflow.json                — User config (auto-created on first run by setup wizard)
```

### CLI commands
```
workflow setup      — interactive wizard: find claude, set data dir, optionally install as service
workflow serve      — foreground server (for dev)
workflow start/stop/restart/status — launchd service management
workflow update     — git pull + rebuild + restart (if installed as service)
```

## Current state (as of 2026-03-05)

**Working:**
- Three-tier kanban board with drag-drop
- Task CRUD (create, edit, mark done, delete, move)
- Sessions: interactive chat with live polling (1.5s), client-side message queue
- Auto-brief: on task creation, a background agent runs and writes findings to `tasks.brief`
- Task context injected into every session as a collapsible system block (not a user bubble)
- Sessions auto-named from first 6 words of prompt
- Brief rendered as markdown (goldmark server-side, marked.parse client-side for polling)
- Brief panel: collapsible, live spinner, ↺ re-run button
- Session list on task page: live refresh every 3s
- Global nav: Tasks / Sessions / Notes
- `/sessions` — flat + grouped-by-task, toggle persisted in localStorage
- `/notes` — stub page
- Friendly error messages when claude not configured
- Send button shows "Queue" label while agent is running

**Not yet implemented (see task list below)**

---

## Testing

### Rules
- **Before committing any substantial change**, run the Playwright suite below and the Go tests.
- Go tests: `cd /root/.nox/workspace/workflow && go test -tags fts5 ./...`
- Playwright suite: spin up test server (see "How Nox works" above), run each scenario, kill server.
- If a test fails, fix the bug before pushing.
- Add new Playwright scenarios whenever a significant new UI feature ships.

---

### Go unit & integration tests

Location: `internal/db/db_test.go`, `internal/models/task_test.go`, etc.

**What to cover:**

- `internal/models/task_test.go` ✅ 2026-03-05 (21 tests, all passing)
  - [x] `ElapsedSeconds()` / `ElapsedLabel()` — running timer, stopped timer, zero
  - [x] `IsOverdue()` / `IsDueToday()` — boundary cases (today, yesterday, tomorrow, nil)
  - [x] `DirectionLabel()` — both values

- `internal/db/db_test.go` ✅ 2026-03-05 (18 tests, all passing — temp file DB, not in-memory)
  - [x] `CreateTask` / `GetTask` / `DeleteTask` round-trip
  - [x] `MarkDone` sets `done=1` and `done_at`
  - [x] `TimerToggle` starts and stops correctly; accumulates elapsed
  - [x] `TimerReset` zeroes `timer_total` and clears `timer_started`
  - [x] Position increments within tier on create
  - [x] `ListTasks` excludes done by default, includes when requested
  - [x] `CreateSession` / `GetSession` / `ListSessions` (filtered by task)
  - [x] `ArchiveSession` / `PinSession`
  - [x] `UpdateBrief` sets brief content and status
  - [x] `GetTaskByPRURL` — found, not found, ignores done tasks
  - [x] `MoveTask` reorders positions correctly ✅ 2026-03-08
  - [x] `ListBriefVersions` returns newest-first ✅ 2026-03-08
  - [x] Notes: `CreateNote` / `UpdateNote` / `ListNotes` / `DeleteNote` ✅ 2026-03-08
  - [x] FTS5 search: `SearchSessions` returns results for indexed message content ✅ 2026-03-08

- `internal/handlers/` — HTTP handler integration tests using `httptest.NewServer` ✅ 2026-03-08 (102 total)
  - [x] `GET /` returns 200
  - [x] `POST /tasks` creates task and redirects (empty title → 400, also fixed bug)
  - [x] `POST /tasks/quick` returns JSON with new task ID
  - [x] `POST /tasks/{id}/done` marks done, redirects to board
  - [x] `POST /tasks/{id}/timer` toggles timer, returns updated elapsed
  - [x] `GET /digest` returns 200 with correct week stats
  - [x] `GET /api/notes` returns empty list on fresh DB
  - [x] `POST /api/notes` creates note, returns JSON
  - [x] `PATCH /api/notes/{id}` updates content, derives title from first line

---

### Playwright UI test suite

Run these in order — they share the same test server and DB state within a session.
Each scenario is listed with: **what to do** → **what to verify**.

**Setup** (before all tests)
```
build /tmp/workflow-test
mkdir /tmp/wf-test-data
start server on :7071
```

**Teardown** (after all tests)
```
kill server, rm /tmp/workflow-test, rm -rf /tmp/wf-test-data
```

---

#### P01 — Board loads
- Navigate to `http://localhost:7071/`
- ✓ Three columns visible: Today, This Week, Backlog
- ✓ Nav links: Tasks, Sessions, Notes, Digest

#### P02 — Create task (full form)
- Click `+ New task`
- Fill title: "Test PR review", type: PR Review, tier: Today
- Click `Add task`
- ✓ Redirected to task detail page
- ✓ Title visible, work type badge shown

#### P03 — Board shows task card
- Navigate to board `/`
- ✓ "Test PR review" card visible in Today column
- ✓ No elapsed time shown (timer not started)

#### P04 — Quick-add task
- On board, click `+ Add task` in Backlog column
- Type "Quick backlog item", press Enter
- ✓ Redirected to new task page
- ✓ Title is "Quick backlog item", tier is Backlog

#### P05 — Board filtering
- Navigate to board
- Click "PR Review" filter chip
- ✓ "Test PR review" card still visible
- Click "Coding" filter chip
- ✓ "Test PR review" card hidden

#### P06 — Task timer
- Navigate to "Test PR review" task page
- Click `▶ Start timer`
- ✓ Button changes to `⏹ Stop`, elapsed shows `< 1m`
- Wait 2s
- Click `⏹ Stop`
- ✓ Button reverts to `▶ Start timer`, elapsed label still visible
- ✓ Elapsed label also visible on board card (P07 verifies)

#### P07 — Elapsed shown on board card
- Navigate to board `/`
- ✓ "Test PR review" card shows elapsed time label (e.g. `< 1m`)

#### P08 — Notes: create and auto-save
- Navigate to `/notes`
- Click `+ New`
- ✓ Editor appears, sidebar shows "Untitled note"
- Type `# My note\n\nSome content here`
- Wait 1.5s for debounce
- ✓ Sidebar title updates to "My note"

#### P09 — Notes: preview mode
- On notes page with content from P08
- Click `Preview`
- ✓ `<h1>My note</h1>` visible in preview pane
- Click `Edit`
- ✓ Textarea visible again

#### P10 — Weekly digest: in-progress
- Navigate to `/digest`
- ✓ "this week" badge present
- ✓ Stats bar shows ≥ 1 in progress (tasks from P02/P04)
- ✓ "Test PR review" visible under In progress

#### P11 — Mark task done → digest updates
- Navigate to "Test PR review" task page
- Click `Mark done`
- ✓ Redirected to board; task not shown
- Navigate to `/digest`
- ✓ Completed count ≥ 1
- ✓ "Test PR review" appears under Completed with ✓

#### P12 — Session search
- Navigate to `/search`
- ✓ Search input visible
- (Search with content is only testable if a session with messages exists — skip for now, note as manual)

#### P13 — Keyboard shortcuts
- Navigate to board `/`
- Press `?`
- ✓ Shortcuts overlay visible
- Press `Esc`
- ✓ Overlay dismissed

---

## Task list

Work through these top-to-bottom. Mark done with ✅ and timestamp. Add new tasks at the bottom of the appropriate section.

### High priority (do first)

- [x] **Session search** ✅ 2026-03-05
  - FTS5 virtual table on messages with insert/update/delete triggers
  - `/search` page with highlighted snippets, linked back to session
  - Search icon in nav (top right) on all pages
  - Results grouped by session (not per-message), ranked by relevance

- [x] **Task quick-capture from board** ✅ 2026-03-05
  - Dashed "+ Add task" button at bottom of each column
  - Inline form: title + work type selector, Enter to submit
  - `POST /tasks/quick` endpoint returns JSON, navigates to new task

- [x] **Pinned sessions** ✅ 2026-03-05
  - Pin/unpin button on session page; pinned sessions sort to top of task's list
  - Accent border on pinned session cards; 📌 icon in list

### Medium priority

- [x] **Session rename** ✅ 2026-03-05
  - Session title is contenteditable in the header
  - Saves on blur/Enter, reverts on Esc or error
  - `PATCH /tasks/{id}/sessions/{sid}/name`

- [x] **Session archive / soft-delete** ✅ 2026-03-05
  - Archive/Unarchive button on session page
  - Archived sessions hidden by default on /sessions and task session list
  - 'Show archived' toggle on /sessions page

- [x] **Task timer / time tracking** ✅ 2026-03-05
  - Start/stop timer on task page; elapsed shown live with 1s tick
  - Timer resets to 0 on demand; elapsed shown on board card when > 0
  - `timer_started` + `timer_total` columns; `ElapsedSeconds()`/`ElapsedLabel()` helpers

- [x] **Keyboard shortcuts** ✅ 2026-03-05
  - `n` new task, `q` quick-add to first column, `s` search, `?` overlay, `Esc` close/clear filter
  - Board page only; ignored when focus is in input/textarea

- [x] **Board filtering** ✅ 2026-03-05
  - Filter chips above the board: All + each work type + On me / Waiting
  - Client-side (no server round-trip), persisted in localStorage
  - Esc clears active filter

- [x] **Task due dates / urgency** ✅ 2026-03-05
  - Optional date field on create/edit forms
  - Red left border + "Xd overdue" label on overdue cards
  - Amber border + "Today" label for tasks due today
  - `IsOverdue()` / `IsDueToday()` helpers on Task model
  - `dueDateLabel()` template func: Yesterday / Today / Tomorrow / day name / Jan 2

### Lower priority / future

- [x] **Webhook endpoint for GitHub PRs** ✅ 2026-03-05
  - `POST /webhooks/github` — HMAC-SHA256 signature verification
  - Handles: opened, reopened, synchronize, ready_for_review
  - Creates PR Review task in Today on open; re-briefs on sync
  - Deduplicates by PR URL; skips draft PRs
  - `webhook_secret` in workflow.json

- [x] **Brief versioning** ✅ 2026-03-05
  - Each completed brief run stored in `brief_versions` table
  - History panel on task page (shows N versions, collapsible per version)

- [x] **Multiple agent providers** ✅ 2026-03-06
  - Currently only ClaudeLocal
  - Add ClaudeAPI (direct Anthropic API, for when claude CLI not available)
  - Provider selected per-session in config or UI

- [ ] **Sub-sessions / thread view**
  - Sessions can spawn sub-sessions (parent_id is already in schema)
  - UI for this is a future concern

- [x] **Per-task scratchpad** ✅ 2026-03-06
  - Lightweight textarea on task page (between timer and agent brief)
  - Auto-saves on 800ms debounce; Cmd/Ctrl+S to force; "saving…"/"saved" status
  - `scratchpad TEXT` column on tasks; GET/PATCH `/api/tasks/{id}/scratchpad`
  - Resize: vertical; placeholder guides usage

- [x] **Notes page** ✅ 2026-03-05
  - Two-pane editor: sidebar list + full-height textarea
  - Auto-save with 800ms debounce; Cmd/Ctrl+S to force save
  - Preview mode toggle (rendered markdown via marked.js)
  - REST API: GET/POST/PATCH/DELETE /api/notes; title auto-derived from first line
  - Global notes (task_id='') accessible from nav

- [x] **Weekly digest view** ✅ 2026-03-05
  - `/digest` page: week navigation (prev/next), stats bar (completed/in-progress/sessions/time), task lists
  - Linked from nav; shows completed ✓ and in-progress ○ tasks with time tracked and session counts

---

## Completed ✅

### Completed 2026-03-05 (continued)

- [x] Bootstrap setup command — interactive wizard, auto-detects claude, writes config, installs launchd service
- [x] Daemon management — `workflow start/stop/restart/status` via launchctl
- [x] `workflow update` — git pull + rebuild binary in place + restart service
- [x] `workflow serve` subcommand with `-dir` flag (data dir consolidation)
- [x] README and RUNNING.md rewritten for new setup flow

### Completed 2026-03-05

- [x] Hide/style injected task context in chat — collapsible system block, not a user bubble
- [x] Render brief as markdown (goldmark server-side, marked.parse client-side)
- [x] Sessions auto-named from first 6 words of prompt
- [x] CLAUDE_ALLOW_ROOT: gated behind WORKFLOW_DEV_ROOT=1 env var
- [x] Global /sessions page: flat list + "by task" grouped view, view toggle in localStorage
- [x] Global /notes page stub
- [x] Shared nav bar across all pages (Tasks / Sessions / Notes)
- [x] Session list live refresh on task page (every 3s)
- [x] Collapsible brief panel, state persisted in localStorage
- [x] Brief poll cleanup on page unload
- [x] Better error messages for missing claude CLI (inline in session form, friendly brief error)
- [x] Send button shows "Queue" while agent is running
- [x] README rewritten to reflect current state
- [x] RUNNING.md created — server setup, build, run, workarounds documented

---

## New features (proposed 2026-03-07)

### Medium priority

- [x] **Task dependencies** ✅ 2026-03-07
  - `blocked_by TEXT` column on tasks; SetBlockedBy/ClearBlockedBy/GetBlockerTask DB methods
  - Search endpoint GET /api/tasks?q= for dropdown; POST/DELETE /api/tasks/{id}/blocked-by
  - Task page dependencies panel with live search + clear button
  - Board cards: ⛔ badge + opacity:0.7 when blocked; cascade-clear on MarkDone
  - 33 tests passing

- [x] **Recurring tasks** ✅ 2026-03-07
  - `recurrence TEXT` column; CloneTaskForRecurrence on MarkDone
  - Recurrence select on create/edit; ↻ badge on board cards and task page
  - Flash banner on board when clone created; 41 tests passing

- [x] **Task templates** ✅ 2026-03-08
  - `task_templates` table, 4 default seeds (PR Review, Deployment, Weekly Sync, Design Review)
  - `/templates` page, "Start from template" dropdown on new task form, "Save as template" on task page
  - JSON API for JS dropdown; 51 tests passing

### Lower priority

- [x] **Nav layout fix — consistent top bar across all tabs** ✅ 2026-03-08
  - Button now always rendered with `visibility: hidden` on non-board tabs — tab bar no longer shifts

- [x] **CSV export** ✅ 2026-03-08
  - `GET /export/tasks.csv` — all tasks, 12 columns including time_tracked, status, recurrence, blocked_by
  - Subtle export link at bottom of Digest tab; 68 tests passing

- [x] **Keyboard navigation on board** ✅ 2026-03-08
  - Arrow keys navigate cards, Enter opens, `m` moves to next column (DOM surgery, no reload)
  - Indigo focus ring; respects filter bar, overlay guard, existing shortcuts; 3 new handler tests

---

## New features (proposed 2026-03-08)

### Medium priority

- [x] **Task comments / activity log** ✅ 2026-03-08
  - Lightweight per-task comments: text entry, timestamp, author ("You")
  - Displayed below the agent brief on the task page, newest-first or chronological toggle
  - Useful for jotting manual notes, decisions, follow-ups alongside the agent session
  - `task_comments` table (task_id, body, created_at); `GET/POST /api/tasks/{id}/comments`

- [x] **Bulk board actions** ✅ 2026-03-08
  - Checkbox on hover per card; shift-click range select within column
  - Floating sticky toolbar: count + Move to dropdown + Mark done + Clear
  - Sequential DOM surgery (no reload); fixed jsonArr bug (was 500 on tasks with tags); 141 tests

- [x] **Task labels / tags** ✅ 2026-03-08
  - `task_tags` join table; AddTag/RemoveTag/ListTags/ListAllTags DB methods; no N+1 (bulk hydration)
  - Tag chips on task page (autocomplete) + board cards (up to 3, +N overflow); board filter by tag
  - 141 tests passing

### Lower priority

- [x] **Notification / reminder system** ✅ 2026-03-08
  - `task_reminders` table; CreateReminder/ListDueReminders/MarkReminderSent/Delete
  - Reminders panel on task page (datetime-local input + note); past state styling
  - In-app toast delivery: `GET /api/reminders/due` + `POST /api/reminders/{id}/dismiss`; client polls every 60s; hover-to-pause, CSS progress bar, sessionStorage dedup; 186 tests

- [ ] **Sub-sessions / thread view**
  - Sessions can spawn sub-sessions (parent_id is already in schema)
  - UI for this is a future concern — defer until a concrete use case emerges

---

## New features (proposed 2026-03-08 evening)

### Medium priority

- [x] **Task search** ✅ 2026-03-08
  - FTS5 on title/description/scratchpad; `GET /search/tasks?q=...`; snippet with `<mark>` highlights
  - Tabbed search page (Tasks | Sessions), tab carries `?q=` across; `s` shortcut → task search; 178 tests

- [x] **Recurring task calendar view** ✅ 2026-03-08
  - `GET /calendar` week view; Mon–Sun grid; `?week=N` navigation; overdue banner; month-span label
  - "Calendar" nav link added; 198 tests

- [x] **Task age indicator** ✅ 2026-03-08
  - `DaysInColumn()`, `AgeLabel()`, `AgeClass()` on Task model; midnight-truncated for whole-day accuracy
  - age-fresh (<2d, grey) / age-warn (2-5d, amber) / age-stale (≥6d, red); right-aligned on board cards; 165 tests

### Lower priority

- [x] **Command palette** ✅ 2026-03-09
  - ⌘K / Ctrl+K from any page; default: 8 quick-nav actions; live task search (debounced 150ms, `/api/tasks`)
  - Arrow keys + Enter + Esc; tier pills on results; backdrop blur; global via nav.html

- [x] **Agent session quality feedback** ✅ 2026-03-09
  - `feedback TEXT` column on sessions; `SetSessionFeedback` DB method
  - `POST /tasks/{id}/sessions/{sid}/feedback` — "up", "down", or "" to clear
  - Thumbs up/down in session header; toggle off on second click; accent colour when active; 202 tests passing

---

## New features (proposed 2026-03-09)

### High priority

- [x] **Daily standup generator** ✅ 2026-03-09
  - One-click "What did I work on today?" on the Digest tab
  - Queries: tasks moved/completed today, sessions started today (titles + brief summaries), time tracked
  - Formats as: "Yesterday: ..., Today: ..., Blockers: ..." (standard standup format)
  - Copy-to-clipboard button; optional "regenerate" to vary wording via agent
  - `GET /api/standup?date=YYYY-MM-DD` returns JSON; `/standup` page with copy button
  - No new DB columns needed — uses existing task/session/comment data

### Medium priority

- [x] **Brief diff view** ✅ 2026-03-09
  - When a PR brief is re-run (e.g. after a `synchronize` webhook), show a diff vs previous version
  - Highlight added/removed lines in brief versions panel (green/red, like a code diff)
  - Useful for tech lead reviewing what changed in a PR update
  - `GET /api/tasks/{id}/briefs/diff?from=N&to=M` returns unified diff
  - Rendered in brief history panel with toggle between raw and diff views

- [x] **Session export as markdown** ✅ 2026-03-09
  - "Export" button on session page → downloads `session-{id}.md`
  - Format: task title, session name, date, full message history (role: content blocks)
  - Useful for sharing AI code reviews or analysis with teammates
  - `GET /tasks/{id}/sessions/{sid}/export.md`


---

## New features (proposed 2026-03-09 nudge)

### Medium priority

- [x] **Webhook / GitHub PR sync** ✅ 2026-03-10
  - Receive GitHub webhooks for `pull_request` events (opened, synchronize, closed, merged)
  - Auto-create PR Review tasks on `opened`; auto-update title/URL on `synchronize`
  - Auto-mark done on `closed`/`merged`
  - Config: `github_webhook_secret` in workflow.json; single endpoint `POST /webhooks/github`
  - Reduces manual task creation for PR workflows

- [x] **Session pinning to task top** ✅ 2026-03-10
  - Pin a session as the "canonical review" for a task — shown prominently on task page above others
  - Useful when a task has many sessions but one is the definitive output (e.g. final brief)
  - `pinned BOOLEAN` already exists on sessions table — just needs UI promotion

- [x] **Time-boxed focus mode** ✅ 2026-03-09
  - Start a focus timer for a task (25min Pomodoro or custom duration)
  - Timer shown in page title + floating badge; notification when done
  - Increments task's time_tracked automatically when timer completes
  - No backend needed — pure JS with localStorage persistence

- [x] **Board swimlanes by assignee / tag** ✅ 2026-03-10
  - Current board has Today / This Week / Backlog columns
  - Add optional row grouping by tag (e.g. group all "frontend" tasks in a swimlane)
  - Toggle swimlane view; collapse/expand per lane

### Lower priority

- [x] **Task duplication** ✅ 2026-03-10
  - "Duplicate" button on task page — copies title, description, work type, tags, recurrence
  - Useful for templated tasks that don't fit the formal template system
  - Simple `POST /api/tasks/{id}/duplicate` → redirects to new task

- [x] **Keyboard shortcut cheatsheet** ✅ 2026-03-10
  - `?` key already bound to toggle help modal (stub)
  - Fill in the actual shortcuts: board nav, `n` new task, `s` search, `⌘K` palette, etc.


---

## New features (proposed 2026-03-10 nudge)

### Medium priority

- [x] **Inline title / description editing** ✅ 2026-03-10
  - Click task title on task view → editable input; Enter/blur saves via PATCH /api/tasks/{id}
  - Click description → textarea with auto-grow; blur saves; Esc cancels
  - "Add description…" placeholder shown when empty; click to start editing
  - PATCH /api/tasks/{id} endpoint + PatchTaskFields() DB method
  - 3 new integration tests

- [x] **Markdown task descriptions** ✅ 2026-03-10
  - Task description now rendered by marked.js (same pipeline as agent briefs)
  - Raw markdown preserved in data attribute for re-editing

- [x] **Tree-sitter syntax highlighting (modal-editor)** ✅ 2026-03-10
  - Implemented in modal-editor/src/syntax/mod.rs
  - Rust, Python, JavaScript/TS, Go, Lua — auto-detected from file extension
  - Wired into open_file() and save_active(); 8 tests

- [x] **Nucleo fuzzy matching in file picker (modal-editor)** ✅ 2026-03-10
  - Replaced hand-rolled subsequence matcher with nucleo-matcher
  - Results sorted by descending score (best match first)

## New features (proposed 2026-03-10 nudge 2)

### Medium priority

- [x] **Task effort estimate (XS/S/M/L/XL)** ✅ 2026-03-10
  - New `effort` TEXT column on tasks (auto-migration)
  - Select on create/edit form: XS (<1h), S (1-3h), M (half-day), L (full-day), XL (2+ days)
  - Effort badge on board cards (teal/blue/purple/pink scale, distinct from priority)
  - Effort badge on task view header
  - data-effort attribute on board cards
  - `effortLabel` + `EffortPoints()` template/model methods
  - CSV export includes priority + effort columns
  - 3 integration tests; all 237 tests passing

- [x] **LSP client (modal-editor)** ✅ 2026-03-10
  - stdio transport with Content-Length framing
  - initialize/initialized handshake; did_open/did_change/did_close
  - hover request (returns Option<String>)
  - publishDiagnostics broadcast via LspEvent enum
  - EditorCore.lsp field; open_file notifies server
  - language_id_for_path helper (12 extensions)
  - 12 tests covering wire encoding, language IDs, dispatch, reader task

## New features (proposed 2026-03-10 nudge 3)

### Medium priority

- [x] **LSP diagnostics + hover popup (modal-editor)** ✅ 2026-03-10
  - EditorCore.diagnostics: HashMap<uri, Vec<Diagnostic>>
  - apply_diagnostics() / active_diagnostics() / error_count() / warning_count()
  - Renderer: diag_ranges_for_line() underlines errors (red) and warnings (orange)
  - Status bar E:N W:N indicator
  - Hover popup: K in Normal mode, floating widget, dismisses on any other key
  - Tokio runtime in run() for async LSP calls; lang_server_cmd() auto-spawns server
  - 5 new tests (diagnostics storage, counts, replace)

### Lower priority

- [x] **Tab title badge** ✅ 2026-03-10
  - Board page: '(N) workflow' in browser tab when P1 or overdue tasks present
  - Resets to 'workflow' when all clear; updates on load + filter changes
  - Zero backend — pure JS

- [x] **Priority/effort in weekly digest** ✅ 2026-03-10
  - DigestTask.Priority + Effort fields; Done/InProgress DB queries select them
  - Priority/effort badges (reuse existing card-tag CSS) on Done and InProgress rows in digest template
  - InProgress sorted by priority first (P1→P2→P3→unset), then updated_at desc
  - DaysInColumn computed on WaitingOnOthers items

- [x] **Velocity comparison in digest stats** ✅ 2026-03-10
  - DigestWeek.DoneLastWeek + TimeDeltaPct populated from prior-week query
  - Completed stat shows ↑/↓/→ arrow with "N last wk" label when prior data available

- [x] **Starred tasks** ✅ 2026-03-11
  - `starred INTEGER` column + idempotent migration
  - `DB.StarTask()` toggle, `DB.ListStarredTasks()`
  - `PATCH /api/tasks/{id}/star` returns `{"starred": bool}`
  - ☆/★ button on task page; amber border + badge on board cards
