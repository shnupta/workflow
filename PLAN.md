# workflow — Build Plan

This is the canonical task tracker for the workflow app.
Nox reads and updates this file when working on the project overnight or between sessions.

## How Nox works on this repo

- Repo lives at `/root/.nox/workspace/workflow/` on the server
- Go is at `/usr/local/go/bin/go` — always use `export PATH=$PATH:/usr/local/go/bin` before running go commands
- Build check: `cd /root/.nox/workspace/workflow && go build ./...`
- After each change: `go build ./...` to verify, then `git add -A && git commit -m "..." && git push`
- Casey pulls and restarts the binary on his machine to pick up changes
- Can't run the app on the server — build + push is the workflow

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

## Current state (as of 2026-03-04)

**Working:**
- Three-tier kanban board with drag-drop
- Task CRUD (create, edit, mark done, delete, move)
- Sessions: interactive chat with live polling (1.5s), client-side message queue
- Auto-brief: on task creation, a background agent runs and writes findings to `tasks.brief`
- Task context injected into every session prompt (title, description, type, PR URL, brief)
- Sessions sorted by updated_at DESC
- Brief panel on task page with live spinner + ↺ re-run button
- Provider-agnostic agent abstraction (ClaudeLocal is the only impl)

**Known issues / rough edges:**
- Task context block (`## Task context\n...`) appears as a visible user message in the chat — should be stripped or shown differently
- Session names are auto-generated as "Session Mar 4 22:14" — should use first few words of prompt
- Session list on task page is server-rendered (static) — doesn't update live after new sessions created without a page refresh
- Brief content not rendered as markdown in the brief panel — shows raw markdown text

---

## Task list

Work through these top-to-bottom. Mark done with ✅ and timestamp. Add new tasks at the bottom.

### High priority (do first)

- [x] **Hide/style injected task context in chat** ✅ 2026-03-05
  - Stored as `role=system, kind=context` — renders as collapsible block, not a chat bubble
  - User's actual prompt stored separately and displayed normally
  - `buildTaskContext()` handles context; `RunSession` takes optional `visiblePrompt` arg

- [x] **Render brief as markdown** ✅ 2026-03-05
  - Added `goldmark` for server-side markdown → HTML rendering
  - `markdownHTML` template func renders brief safely
  - Polled briefs use `marked.parse()` client-side

- [x] **Auto-name sessions from prompt** ✅ 2026-03-05
  - `sessionNameFromPrompt()`: first 6 words, max 48 chars

- [x] **Fix CLAUDE_ALLOW_ROOT** ✅ 2026-03-05
  - Removed `--dangerously-skip-permissions` (blocked on root)
  - Set `CLAUDE_ALLOW_ROOT=1` env var — works correctly

### Medium priority

- [x] **Session list live refresh on task page** ✅ 2026-03-05
  - Polls `/tasks/{id}/sessions` every 5s, re-renders only when something changed

- [x] **Collapsible brief on task page** ✅ 2026-03-05
  - Click brief header to collapse/expand; state persisted in localStorage per task

- [x] **Brief polling cleanup** ✅ 2026-03-05
  - `beforeunload` clears both brief and session poll timers

- [x] **Global nav + Sessions index page** ✅ 2026-03-05
  - Tasks / Sessions / Notes nav across all pages (Claude Code added this during auto-brief run)
  - `/sessions` shows all sessions grouped by task
  - Session page has breadcrumb back to task

- [ ] **Better error display when claude not configured**
  - Currently shows raw error message; should be friendlier

- [x] **Send button shows "Queue" while agent running** ✅ 2026-03-05
  - Button label changes to "Queue" at 60% opacity while agent is busy

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

- [x] **Update README** ✅ 2026-03-05
  - Fully rewritten to reflect current state

---

## Completed ✅

- Sessions DB schema + migrations (brief, brief_status columns added safely)
- Provider-agnostic agent abstraction (Runner interface, Event types)
- ClaudeLocal runner (stream-json, --verbose, stderr capture, event normalisation)
- RunSession driver (normalises events → DB messages, manages status transitions)
- Interactive sessions with live polling chat (1.5s interval)
- Client-side message queue (cancel, edit inline, drain on agent idle)
- Auto-brief on task creation (PR review prompt, generic prompt)
- Task context injected into session prompt (title, desc, type, PR URL, brief)
- Fire-and-forget mode removed — all sessions are interactive
- GitHub token / Anthropic API key / PR prompt config removed
- Sessions sorted by updated_at DESC (most recently active first)
- Tasks sorted by updated_at DESC within tier
- Request logging middleware
- Deduplication in chat poll (rowid-based, not created_at)
- [brief] sessions filtered from session list
- Mark done button height fix (display:contents on form)
- Enter to send, Shift+Enter for newline in session input

### Completed tonight ✅ (2026-03-05)

- [x] Task context shown as collapsible info block in chat (not a user bubble)
- [x] Brief rendered as markdown (goldmark server-side, marked.parse client-side)
- [x] Sessions auto-named from first 6 words of prompt
- [x] CLAUDE_ALLOW_ROOT: gated behind WORKFLOW_DEV_ROOT=1 env var — safe to push
- [x] Global /sessions page: flat list + "by task" grouped view, view toggle persisted in localStorage
- [x] Global /notes page stub
- [x] Shared nav bar across all pages (Tasks / Sessions / Notes) with active state
- [x] RUNNING.md created — server setup, build, run, workarounds all documented
