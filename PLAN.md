# workflow — Build Plan

This is the canonical task tracker for the workflow app.
Nox reads and updates this file when working on the project overnight or between sessions.

## How Nox works on this repo

- Repo lives at `/root/.nox/workspace/workflow/` on the server
- Go is at `/usr/local/go/bin/go` — always use `export PATH=$PATH:/usr/local/go/bin` before running go commands
- Build check: `cd /root/.nox/workspace/workflow && go build ./...`
- After each change: `go build ./...` to verify, then `git add -A && git commit -m "..." && git push`
- Casey pulls and restarts the binary on his machine to pick up changes
- **Cannot run the app on the server** — build + push is the workflow. No test server.
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

- [ ] **Multiple agent providers**
  - Currently only ClaudeLocal
  - Add ClaudeAPI (direct Anthropic API, for when claude CLI not available)
  - Provider selected per-session in config or UI

- [ ] **Sub-sessions / thread view**
  - Sessions can spawn sub-sessions (parent_id is already in schema)
  - UI for this is a future concern

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
