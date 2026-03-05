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
cmd/workflow/main.go         — entrypoint, HTTP server, request logging
internal/config/config.go    — Config struct, hot-reload watcher (2s poll)
internal/db/db.go            — SQLite, migrations, all DB methods
internal/models/             — Task, Session, Message structs with json+db tags
internal/handlers/
  handlers.go                — task CRUD, auto-brief agent, render helpers
  sessions.go                — session CRUD, chat endpoints, message queue
internal/agent/
  agent.go                   — Runner interface, Event types (provider-agnostic)
  claude_local.go            — Claude CLI runner (stream-json, stderr capture)
  runner.go                  — RunSession: drives agent, writes normalised messages to DB
templates/                   — Go html/template files
static/css/style.css         — All styles (dark theme, CSS variables)
workflow.json                — User config (claude_bin only; created on first run)
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

- [ ] **Session search**
  - Search bar on `/sessions` page (and possibly task page session list)
  - SQLite FTS5 on `messages.content` + `sessions.name`
  - Results show: session name, task name, matching message excerpt, timestamp
  - Implemented as `GET /search?q=...` returning rendered results (HTMX or plain page)
  - Add FTS5 virtual table in migration: `CREATE VIRTUAL TABLE messages_fts USING fts5(content, content='messages', content_rowid='id')`
  - Trigger to keep FTS index in sync on insert

- [ ] **Task quick-capture from board**
  - Inline "Add task" button at the bottom of each column — opens a minimal form (title + type only)
  - Saves immediately, auto-brief fires
  - Avoids full `/tasks/new` page load for quick capture

- [ ] **Pinned sessions**
  - Pin a session to the top of the task's session list
  - Useful when one session becomes the "main" investigation thread
  - `pinned bool` column on sessions table

### Medium priority

- [ ] **Session rename**
  - Click session name in header to edit inline (contenteditable or small input)
  - Saves on blur or Enter
  - `PATCH /sessions/{id}` endpoint

- [ ] **Session archive / soft-delete**
  - Brief sessions (`[brief]`) clutter the sessions list — option to hide or archive them
  - `archived bool` column; `/sessions` and task session list filter them out by default
  - Toggle to show archived sessions

- [ ] **Task timer / time tracking**
  - Start/stop timer on a task
  - Shows elapsed time on card and task page
  - Stored in DB; useful for knowing how long things actually take
  - Could feed a weekly summary view later

- [ ] **Keyboard shortcuts**
  - `n` — new task
  - `s` — focus search
  - `Esc` — close modals / cancel
  - `?` — show shortcuts overlay
  - Most valuable on the board page

- [ ] **Board filtering**
  - Filter by work type (PR Review, Coding, etc.)
  - Filter by "blocked on me" / "blocked on them"
  - Filter persisted in URL params so it's shareable/bookmarkable

- [ ] **Task due dates / urgency**
  - Optional due date on tasks
  - Overdue tasks shown with red highlight on board
  - Could auto-move overdue tasks to Today on startup

### Lower priority / future

- [ ] **Webhook endpoint for GitHub PRs**
  - `POST /webhooks/github` — validates signature, parses PR open/update events
  - Auto-creates or updates a task with PR URL set
  - Auto-brief fires immediately
  - Requires `webhook_secret` in workflow.json

- [ ] **Brief versioning**
  - Store multiple briefs (each re-run) rather than overwriting
  - Show timestamp of when brief was generated

- [ ] **Multiple agent providers**
  - Currently only ClaudeLocal
  - Add ClaudeAPI (direct Anthropic API, for when claude CLI not available)
  - Provider selected per-session in config or UI

- [ ] **Sub-sessions / thread view**
  - Sessions can spawn sub-sessions (parent_id is already in schema)
  - UI for this is a future concern

- [ ] **Notes page**
  - Currently a stub
  - Freeform markdown notes scoped to a task or global
  - Auto-save with debounce (500ms)
  - Could be a second tab on the task page rather than a separate nav item

- [ ] **Weekly digest view**
  - Summary of tasks completed this week, time spent (if timer added), sessions had
  - Read-only view, maybe printable

---

## Completed ✅

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
