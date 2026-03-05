# workflow

A local task board for leads. Captures everything coming at you — PR reviews, deployments, docs, design discussions, chases, approvals — and routes them into a three-tier board (Today / This Week / Backlog) with Claude-powered investigation.

## Quick start

```bash
git clone https://github.com/shnupta/workflow
cd workflow
go build -tags fts5 -o workflow ./cmd/workflow/
./workflow setup
```

`setup` will find Claude, write `~/.workflow/workflow.json`, and install a launchd service so workflow starts automatically on login.

Then open **http://localhost:7070**.

## Requirements

- **Go 1.22+** — [golang.org/dl](https://golang.org/dl)
- **libsqlite3** — `brew install sqlite` on macOS
- **Claude Code CLI** — `npm install -g @anthropic-ai/claude-code`, then `claude` to authenticate

## Commands

```
workflow setup     # first-time setup wizard
workflow status    # is the service running?
workflow start     # start background service
workflow stop      # stop background service
workflow restart   # restart background service
workflow update    # git pull + rebuild + restart
workflow serve     # run in foreground (dev)
```

## GitHub webhook (optional but recommended)

Point a GitHub webhook at `https://your-machine.example.com/webhooks/github` and workflow will automatically create a task whenever a PR is opened against your repos — with auto-brief firing immediately.

**Setup:**
1. In your repo (or org): Settings → Webhooks → Add webhook
2. Set Payload URL to your workflow instance
3. Content type: `application/json`
4. Choose "Let me select individual events" → **Pull requests** only
5. Generate a secret: `openssl rand -hex 20`
6. Add the secret to `~/.workflow/workflow.json`:
   ```json
   { "claude_bin": "...", "webhook_secret": "your-secret-here" }
   ```

**What it does:**
- `opened` / `reopened` / `ready_for_review` → creates a new PR Review task in Today, fires auto-brief
- `synchronize` (new commits pushed) → re-runs the brief on the existing task
- Draft PRs on open are skipped
- If a task for that PR URL already exists, it's updated rather than duplicated

## Updating

```bash
workflow update
```

Pulls latest, rebuilds the binary in place, restarts the service.

## Features

- **Three-tier kanban** — Today / This Week / Backlog. Drag cards between columns.
- **Work types** — PR Review, Deployment, Coding, Design, Docs, Meeting, Approval, Chase, Other. Cards colour-coded by depth (deep / medium / shallow).
- **Auto-brief** — on task creation, Claude investigates automatically. For PR review tasks it reads the PR, identifies risks and focus areas. Shows up on the task page within ~30 seconds, re-runnable.
- **Interactive sessions** — start a chat with Claude on any task. Task context (title, description, PR URL, brief) is injected automatically so Claude is never starting cold. Type-ahead queue so you can keep writing while the agent works.
- **Sessions index** — `/sessions` shows all sessions across all tasks, grouped or flat.
- **Config hot-reload** — edit `~/.workflow/workflow.json` and it picks up changes in 2 seconds.
- **SQLite storage** — one local file, no external services.

## Configuration

`~/.workflow/workflow.json` is created by `setup` with sensible defaults. The only required field is `claude_bin`:

```json
{
  "claude_bin": "/usr/local/bin/claude"
}
```

Work types and tiers are fully customisable. See [RUNNING.md](RUNNING.md) for the full schema.

## Logs

```bash
tail -f ~/.workflow/workflow.log
```
