# RUNNING.md — workflow server setup

## Quick start (first time)

```bash
git clone https://github.com/shnupta/workflow
cd workflow
go build -o workflow ./cmd/workflow/
./workflow setup
```

`setup` will:
- Find your Claude binary automatically (or let you specify it)
- Create `~/.workflow/` with `workflow.db` and `workflow.json`
- Install a launchd service (macOS) so workflow starts on login
- Start the server immediately

Then open **http://localhost:7070**.

---

## Commands

| Command | What it does |
|---|---|
| `workflow setup` | First-time interactive setup wizard |
| `workflow serve` | Run server in foreground (dev mode) |
| `workflow start` | Start background service |
| `workflow stop` | Stop background service |
| `workflow restart` | Restart background service |
| `workflow status` | Show whether service is running |
| `workflow update` | Pull latest, rebuild, restart |

### serve flags

```
workflow serve [flags]
  -addr        Listen address (default: :7070)
  -dir         Data directory — workflow.db + workflow.json (default: ~/.workflow)
  -templates   Template glob override (default: <binary dir>/templates/*.html)
```

---

## Updating to the latest version

```bash
workflow update
```

This runs `git pull`, rebuilds the binary in place, and restarts the launchd service. If you need to set the repo path explicitly:

```bash
WORKFLOW_REPO=/path/to/workflow workflow update
```

---

## Manual install (no launchd)

If you skipped service setup or are on Linux:

```bash
workflow serve
# or with explicit data dir:
workflow serve -dir ~/.workflow
```

To run in background with nohup:

```bash
nohup workflow serve > ~/.workflow/workflow.log 2>&1 &
echo $! > ~/.workflow/workflow.pid

# Stop it:
kill $(cat ~/.workflow/workflow.pid)
```

---

## Logs

If running as a launchd service:

```bash
tail -f ~/.workflow/workflow.log
```

---

## Config (`~/.workflow/workflow.json`)

Only `claude_bin` is required. Everything else has defaults.

```json
{
  "claude_bin": "/path/to/claude",
  "tiers": [
    { "key": "today",     "label": "Today",     "order": 1 },
    { "key": "this_week", "label": "This Week",  "order": 2 },
    { "key": "backlog",   "label": "Backlog",    "order": 3 }
  ],
  "work_types": [
    { "key": "pr_review",  "label": "PR Review",  "depth": "medium" },
    { "key": "coding",     "label": "Coding",     "depth": "deep"   },
    ...
  ]
}
```

Config hot-reloads every 2 seconds — no restart needed after edits.

---

## Development

Build and run locally:

```bash
export PATH=$PATH:/usr/local/go/bin
go build -o workflow ./cmd/workflow/
WORKFLOW_DEV_ROOT=1 ./workflow serve -dir ./testdata -templates './templates/*.html'
```

`WORKFLOW_DEV_ROOT=1` sets `CLAUDE_ALLOW_ROOT=1` for the agent subprocess — needed when running as root (e.g. on a dev server). Never use this flag in production.
