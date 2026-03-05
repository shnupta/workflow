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

- [ ] **Session list live refresh on task page**
  - Currently server-rendered, stale after creating a new session (we navigate away immediately so less critical)
  - But if someone opens task page while brief is running, session list won't show [brief] session (filtered anyway) — this is fine
  - Real need: after creating a session the redirect handles it; but if task page is open in another tab it won't update
  - Fix: poll `/tasks/{id}/sessions` every 5s on task page, re-render list

- [ ] **Collapsible brief on task page**
  - Brief panel is always expanded — add a toggle so it can be collapsed
  - Remember state in localStorage

- [ ] **Brief polling cleanup**
  - `briefPollTimer` keeps running if user navigates away — add `window.addEventListener('beforeunload', () => clearInterval(briefPollTimer))`

- [ ] **Better error display in sessions**
  - If auto-brief fails (claude not configured), show a clear "Claude not configured — set claude_bin in workflow.json" message instead of generic error
  - In `runAutoBrief`: if `runner.Validate()` fails, write a helpful brief_status=error message

- [ ] **Session input UX**
  - Enter to send is now set (Shift+Enter for newline) ✅
  - Add send button disabled state while agent is running (currently just queues)

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

- [ ] **Update README**
  - Outdated — references github_token, anthropic_key, pr_prompt which are all removed
  - New setup is just: install claude CLI, set claude_bin in workflow.json

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
