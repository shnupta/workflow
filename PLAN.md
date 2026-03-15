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

## New features (proposed 2026-03-11 nudge)

### High priority

- [x] **Global activity feed** ✅ 2026-03-11
  - `/activity` page — reverse-chronological stream of all events across tasks, sessions, and comments
  - SQL UNION: tasks created, tasks completed, sessions started, sessions done, task comments
  - Day-grouped timeline with icon-coded event types (＋ created / ✓ done / ▶ session started / ■ done / 💬 comment)
  - `?days=1/3/7/14/30` filter (default 7); Activity tab in nav
  - `ListActivityFeed(numDays, limit)` DB method; `relTimeVal` template func
  - 1 test; 243 total

### Medium priority

- [x] **Sprint goal / weekly target** ✅ 2026-03-11
  - Config.SprintGoal int, Watcher.Patch() for atomic config write-back
  - CountDoneThisWeek() DB method (tasks done since Monday 00:00 UTC)
  - GET/PATCH /api/sprint-goal; board progress bar (fill + green on complete, "🎯 Done!")
  - "Set week goal" dashed button when no goal; clear button when active
  - 1 test; 244 total

- [x] **Dependency graph view** ✅ 2026-03-11
  - GET /dep-graph — pure Canvas 2D (no D3/external deps)
  - ListDepGraph() DB method: nodes + edges for all active blocking relationships
  - Layered left→right layout (layer = longest BFS depth from source)
  - Pan, zoom, hover tooltip, click to open task
  - Node colours: P1=red, P2=amber, P3=green, blocker=amber, normal=blue, done=muted
  - Bezier edge curves with arrowheads; empty state when no deps exist
  - Link from Digest footer; 1 test; 245 total

- [x] **Task comments from board card (quick comment)** ✅ 2026-03-11
  - 💬 button in card actions (opacity 0 → visible on hover/focus)
  - Single shared popover — repositioned to button anchor, click-outside/Esc to dismiss
  - POST /api/tasks/{id}/comments (existing endpoint, zero new backend)
  - Ctrl/Cmd+Enter to submit; "✓ Saved" then auto-close; error states handled
  - 244 tests (no regression)

### Lower priority

- [x] **Activity feed filtering by event type** ✅ 2026-03-11
  - 5 filter chips: All / ✓ Done / ＋ Created / ▶ Sessions / 💬 Comments
  - Pure JS: hides events by data-kind; day dividers hidden when empty
  - Filter persisted in localStorage

- [x] **Activity JSON feed** ✅ 2026-03-11
  - GET /activity.json?days=N — {days, count, events[]}
  - RFC3339 timestamps; task_url for linking; { } button in activity header

## New features (proposed 2026-03-11 nudge 5)

### High priority

- [x] **Session auto-naming from task context** ✅ 2026-03-12
  - New sessions default to "WorkType · Month Day" (e.g. "PR Review · Mar 12")
  - sessionAutoName() in sessions.go; only applied when no name provided

- [x] **Quick task move keyboard shortcut on board** ✅ 2026-03-12
  - 1/2/3 keys move focused card directly to Today/This Week/Backlog
  - moveCardToTier() reuses POST /tasks/{id}/move; updates empty state + col counts

### Medium priority

- [x] **Task "link" field surfaced on board card** ✅ 2026-03-12
  - ↗ anchor in card actions; opens in new tab; only renders when link set

- [x] **Digest: P1/overdue callout** ✅ 2026-03-12
  - DigestWeek.OpenP1Count + OverdueCount; amber/red callout above stats bar

- [x] **Board: comment count badge on card** ✅ 2026-03-12
  - CommentCount on Task model; populateCommentCounts() single GROUP BY query
  - '💬 N' badge on card when N > 0

### Lower priority

- [x] **Notes page: tag organisation** ✅ 2026-03-12
  - tags TEXT column (comma-separated) on notes; ALTER TABLE migration for existing DBs
  - Tag filter chips above note list (All + each tag); /notes?tag=foo
  - Note list items show tag badges; toolbar tags input with auto-save
  - ListNotesByTag(), NoteTags() DB methods; normaliseTags() in handler
  - 245 tests passing

---

## New features (proposed 2026-03-12 nudge)

### Medium priority

- [x] **Task time log / work journal** ✅ 2026-03-12
  - time_logs table: id, task_id, logged_at, duration_mins, note
  - LogTime/ListTimeLogs/DeleteTimeLog DB methods
  - POST /api/tasks/{id}/time-logs, GET same, DELETE /api/time-logs/{id}
  - Time Log panel on task page: form (mins + note), chronological list, delete, empty state
  - autoLogFocusTime() JS hook ready for Pomodoro auto-log
  - 5 new tests; 249 total

- [x] **Bulk status update from digest page** ✅ 2026-03-12
  - POST /api/tasks/{id}/done JSON endpoint (returns {cloned: bool})
  - Digest InProgress rows get a ○ button; click marks done in-place with fade-out
  - Empty-list notice when all tasks cleared; recurring tasks auto-clone
  - 2 tests; 251 total

- [x] **Task templates: auto-suggest on creation** ✅ 2026-03-12
  - When work_type select changes, JS checks for matching template
  - Suggestion banner: "✦ Template available: NAME [Use template] [✕]"
  - Applies template (description, recurrence) or dismisses; pure frontend

### Lower priority

- [x] **Session transcript word count + reading time** ✅ 2026-03-12
  - viewSession computes wordCount + readMins (200wpm, round up); passes to template
  - Session header shows "~N words · X min read" when wordCount > 0
  - Hidden for empty/in-progress sessions

- [x] **Notes: sort by tag** ✅ 2026-03-12
  - ListNotesSorted(taskID, sortBy) DB method; sortBy="tag" → tags ASC, updated_at DESC
  - /notes?sort=tag; sort bar in sidebar (Recent | ↕ Tag); sort preserved with tag filter


---

## New features (proposed 2026-03-12 nudge 2)

### Medium priority

- [x] **Task archiving** ✅ 2026-03-12
  - archived INTEGER column (migration); ArchiveTask(id, archived) DB method
  - Board queries exclude archived tasks via COALESCE(archived,0)=0
  - POST /api/tasks/{id}/archive endpoint; Archive button on task page with confirm dialog
  - Redirects to board on success; archived tasks hidden from all board views

- [x] **Weekly velocity sparkline in digest** ✅ 2026-03-12
  - RecentWeeklyVelocity(8) DB method; VelocitySparkline on DigestWeek
  - sparklineSVG template func: 80×20px SVG; past weeks muted, current week accented
  - Rendered inline in digest 'completed' stat label; 224 tests passing

- [x] **"Chase needed" section in daily standup** ✅ 2026-03-12
  - WaitingStandupTasks() DB method: direction='blocked_on_them', not done, 3+ days old
  - Standup page shows "⏰ Chase needed" amber section when waiting items exist
  - Also appended to standup text for copy-paste; today-only (not historical days)

### Lower priority

- [x] **Session message count badge on task page** ✅ 2026-03-12
  - Session.MessageCount field; ListSessions LEFT JOIN messages COUNT
  - "N msg" badge on unpinned session cards when N > 0; CSS pill style

- [x] **Pomodoro auto-log integration** ✅ 2026-03-12
  - focusStartedMins tracks duration; focusDone() calls autoLogFocusTime(focusStartedMins)
  - Auto-creates time log entry "Focus timer" with correct duration on completion
  - Zero backend change; pure JS wiring


---

## New features (proposed 2026-03-12 nudge 3)

### Medium priority

- [x] **Archive page + unarchive** ✅ 2026-03-12
  - GET /archive — lists all archived tasks in a table (title/tags, type chip, priority badge, last updated)
  - Restore button: POST /api/tasks/{id}/archive {archived:false}, fades row out in-place
  - Delete button: DELETE /tasks/{id} with confirm, fades row out in-place
  - ListArchivedTasks() DB method; archive-page CSS; Archive nav link after Activity
  - 2 new handler tests; 226 total green


---

## New features (proposed 2026-03-12 nudge 4)

### Medium priority

- [x] **WIP limit warning** ✅ 2026-03-12
  - Config.WIPLimit (default 5); passes to board template via data-wip-limit attr
  - Amber warning banner when Today column has > N visible cards
  - MutationObserver re-checks on drag-drop; dismiss button; board-updated event hook
  - Zero backend; CSS consistent with overdue palette; 226 tests green

---

## New features (proposed 2026-03-12 nudge 5)

### Medium priority

- [x] **Inline card title editing (double-click)** ✅ 2026-03-12
  - Double-click task title on board → contenteditable span
  - Enter/blur saves via PATCH /api/tasks/{id}, Escape cancels
  - Draggable disabled on card during edit, restored on finish
  - No backend change needed (endpoint already existed)
  - 226 tests green

---

## New features (proposed 2026-03-12 nudge 6)

### Medium priority

- [x] **Scratchpad overlay from board (e key)** ✅ 2026-03-12
  - Press 'e' on focused board card → modal overlay with textarea
  - Loads GET /api/tasks/{id}/scratchpad; auto-saves on 600ms debounce
  - Ctrl/Cmd+S force-save; Esc/click-outside to close; saving.../saved status
  - Keyboard cheatsheet updated; monospace textarea; 226 tests green

---

## New features (proposed 2026-03-13 nudge 7)

### Medium priority

- [x] **Active session indicator on board cards** ✅ 2026-03-13
  - populateActiveSessions() DB method: queries sessions WHERE status IN ('running','idle')
  - Task.HasActiveSession bool populated by ListTasks alongside tags + comment counts
  - Pulsing green dot (●) in card header when active session exists
  - CSS @keyframes session-pulse: glow + opacity animation, 2s loop
  - 226 tests green

---

## New features (proposed 2026-03-13 nudge 8)

### Medium priority

- [x] **Focus mode (f key)** ✅ 2026-03-13
  - Press 'f' to collapse board to Today column only, full width
  - Indigo badge: "⚡ Focus mode — showing Today only" + Exit button
  - localStorage persistence ('board-focus-mode')
  - toggleFocusMode() restores all columns on exit; grid-template-columns restored
  - Keyboard cheatsheet updated; 226 tests green

---

## New features (proposed 2026-03-13 nudge 9)

### Medium priority

- [x] **Last session timestamp on board cards** ✅ 2026-03-13
  - populateLastSessions() DB method: MAX(created_at) per task from sessions table
  - Task.LastSessionAt *time.Time populated by ListTasks() alongside other per-task data
  - Card footer shows "session Xh ago" / "session Jan 2" when no timer/due date override
  - Uses existing relTime() (nil-safe); helps spot neglected tasks
  - 226 tests green

---

## New features (proposed 2026-03-13 nudge 10)

### Medium priority

- [x] **Compact board mode (c key)** ✅ 2026-03-13
  - Press 'c' to collapse cards to essentials (title + badges only)
  - Hides .task-desc, .card-tags, age labels, timestamps, done button
  - Teal compact-mode-badge at top with Exit button
  - toggleCompactMode() + applyCompactMode(); localStorage persistence
  - CSS .board.compact selectors; distinguishable teal vs indigo focus badge
  - Keyboard cheatsheet updated; 226 tests green

---

## New features (proposed 2026-03-13 nudge 11)

### Medium priority

- [x] **Task page keyboard shortcuts: priority (p) + effort (E)** ✅ 2026-03-13
  - 'p' cycles priority none→P1→P2→P3→none on task detail page
  - 'E' (Shift+E) cycles effort none→XS→S→M→L→XL→none
  - PATCH /api/tasks/{id} on each cycle; badge updates in-place without reload
  - Toast notification at bottom confirms the change (fades 1.8s)
  - Input-focus guard so typing in fields is unaffected
  - 226 tests green

---

## New features (proposed 2026-03-13 nudge 13)

### Medium priority

- [x] **GitHub URL paste detection** ✅ 2026-03-13
  - Paste a github.com PR/issue URL into title → auto-fills title ("PR #N: repo"),
    link field, pr_url field (PRs), selects "PR Review" work type
  - Works in both full task form and board quick-add popover
  - Indigo flash on title input as visual confirmation
  - Regex: /github.com/{owner}/{repo}/(pull|issues)/{number}
  - 226 tests green

---

## New features (proposed 2026-03-13 nudge 15)

### High priority

- [x] **Task due-date reminder badge on board cards** ✅ 2026-03-13
  - 🗓 badge in card header: blue (>2 days), amber (today/tomorrow), red (overdue)
  - IsDueSoon() method added to Task model (diff 0-1 days, not done)
  - tag-due / tag-due-soon / tag-due-overdue CSS; visible even in compact mode
  - 6 model tests for IsDueSoon; 266 total green

### Medium priority

- [x] **Comments endpoint test coverage** ✅ 2026-03-13
  - Already covered in integration_test.go (8 tests: ListComments, CreateComment x4, DeleteComment x3)
  - No new tests needed — item was stale

- [x] **Task labels: add/remove via API tests** ✅ 2026-03-13
  - POST /api/tasks/{id}/tags body `{"tag":"foo"}` → adds tag
  - DELETE /api/tasks/{id}/tags/{tag} → removes
  - Verifies tag appears in ListTaskTags response

### Lower priority

- [x] **Digest page: clicking a task title navigates to task** ✅ 2026-03-13
  - Already implemented — all rows in Completed/InProgress/WaitingOnOthers sections are <a href="/tasks/{id}"> links
  - Item was stale, no change needed

## New features (proposed 2026-03-14 nudge 29)

### High priority

- [x] **Blocked tasks dashboard** ✅ 2026-03-14
  - `/blocked` page: table of all non-done tasks with `blocked_by` set; shows blocker title + link
  - One-click "Unblock" button: `POST /api/tasks/{id}/unblock` clears `blocked_by`, row fades out
  - `ListBlockedTasks()` DB method: LEFT JOIN on blocker title, excludes done+archived
  - `CountBlockedTasks()` DB method: counts for DigestWeek
  - `DigestWeek.BlockedCount`: shown in digest alert bar as `⛔ N blocked` → `/blocked`
  - `⛔ Blocked` filter chip on board: filters cards with `.blocked` CSS class (red accent)
  - "Blocked" nav link added
  - 6 new tests; 295 total green

## New features (proposed 2026-03-15 nudge 31)

### High priority

- [x] **"Blocking" section on task view** ✅ 2026-03-15
  - When a task is a blocker for other tasks, the Dependencies panel shows
    a 🔒 "Blocking:" section listing those downstream tasks with links
  - `ListTasksBlockedBy(id)` DB method: returns non-done/non-archived tasks
    whose `blocked_by == id`, ordered by position
  - `viewTask()` passes `BlockingTasks` to template; amber border + tier/priority badges
  - Bidirectional dependency visibility: you can now see both what you're blocked on
    and what you're blocking, without leaving the task page
  - **Bug fix:** `TestWeeklyDigest_CycleTime` crashed on Sundays (weekday arithmetic
    treated Sunday=0 causing weekStart to land in the future); fixed + guarded
  - 5 new tests + 1 fixed; 295→300 total

## New features (proposed 2026-03-15 nudge 32)

### High priority

- [x] **/stats page** ✅ 2026-03-15
  - GET /stats: KPI row (open count, all-time done, avg open age), open by tier (bars),
    open by priority (colour-coded bars), open by effort (bars), work-type table
    (open+done columns), sessions sparkline (bar chart last 7 days)
  - `GetTaskStats()` DB method (6 aggregate queries)
  - `barPct()` + `sparkBarHeight()` template helpers
  - Nav link "Stats"; 6 new DB tests (300→306)

- [x] **⚡ Quick wins board filter** ✅ 2026-03-15
  - Green filter chip: shows only tasks with effort=xs or effort=s
  - Zero backend — uses existing `data-effort` on cards
  - Surfaces easy-to-complete items without leaving the board
