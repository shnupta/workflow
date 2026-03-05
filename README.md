# workflow

A local web app for managing work inflows as a lead/IC. Captures everything coming at you — PR reviews, deployments, docs, design discussions, chases, approvals — and routes them into a three-tier board (Today / This Week / Backlog) with agent-powered investigation.

## Features

- **Three-tier kanban board** — Today, This Week, Backlog. Drag cards between columns.
- **Work types** — PR Review, Deployment, Coding, Design, Docs, Meeting, Approval, Chase, Other. Cards colour-coded by depth.
- **Blocked on me vs them** — visually distinguish what's in your court vs waiting on someone.
- **Auto-brief** — on task creation, an agent (Claude) automatically investigates the task and writes a brief. For PR review tasks it reads the PR, identifies risks and focus areas. Visible immediately on the task page.
- **Interactive sessions** — start a chat session with an agent on any task. Full task context (title, description, PR URL, brief) is injected automatically. Message queue so you can type ahead while the agent works.
- **Global sessions view** — `/sessions` shows all sessions across all tasks.
- **Single config file** — minimal config in `workflow.json`.
- **SQLite storage** — one local file, no external services.

## Setup

```bash
git clone https://github.com/shnupta/workflow
cd workflow
go build -o workflow ./cmd/workflow/
```

Create `workflow.json`:
```json
{
  "claude_bin": "/path/to/claude"
}
```

Then run:
```bash
./workflow
```

Open [http://localhost:7070](http://localhost:7070).

## Requirements

- Go 1.22+
- libsqlite3 (`brew install sqlite` on macOS, `apt install libsqlite3-dev` on Linux)
- [Claude Code CLI](https://docs.anthropic.com/claude-code) — `npm install -g @anthropic-ai/claude-code`, then `claude` to authenticate

## CLI flags

```
./workflow [flags]

  -addr       Listen address (default: :7070)
  -db         SQLite database path (default: ./workflow.db)
  -templates  Template glob (default: ./templates/*.html)
  -config     Config file path (default: ./workflow.json)
```

## Configuration (`workflow.json`)

Created automatically on first run with defaults. Only `claude_bin` is required.

```json
{
  "claude_bin": "/usr/local/bin/claude",
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

**`depth`** controls card border colour: `deep` = purple, `medium` = amber, `shallow` = grey.

## How it works

### Auto-brief
When you create a task, an agent session runs immediately in the background. For PR review tasks, it's given a detailed prompt: read the PR, understand the changes, identify risks, focus areas, and anything that looks off. The brief appears on the task page within ~30 seconds and can be re-run with the ↺ button.

### Sessions
Click "+ New session" on any task page to start an interactive chat with the agent. The agent automatically receives the task's full context — title, description, PR URL, and the auto-brief — so it's never starting cold. You can queue up messages while the agent is working; they drain one at a time.

The agent has full tool access (bash, file read/write, web fetch, etc.) so it can do real investigation work, not just answer questions.
