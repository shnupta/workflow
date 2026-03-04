# workflow

A local web app for managing work inflows as a lead/IC. Captures everything coming at you — PR reviews, deployments, docs, design discussions, chases, approvals — and routes them into a three-tier board (Today / This Week / Backlog) with a focus on what's blocking you vs what you're waiting on.

## Features

- **Three-tier kanban board** — Today, This Week, Backlog. Drag cards between columns.
- **Work types** — PR Review, Deployment, Doc, Design, Coding, Timeline, Approval, Chase, Meeting, Misc. Cards colour-coded by depth (deep / medium / shallow).
- **Blocked on me vs them** — tasks waiting on someone else are visually dimmed.
- **PR analysis** — paste a GitHub PR URL, click Analyse, and Claude fetches the full diff and returns a markdown-rendered review brief.
- **Single config file** — everything (credentials, board structure) lives in `workflow.json`.
- **SQLite storage** — one local file, no external services.

## Setup

```bash
git clone https://github.com/shnupta/workflow
cd workflow
./setup.sh
```

Edit `workflow.json` to add your API keys, then run:

```bash
./workflow
```

Open [http://localhost:7070](http://localhost:7070).

## Requirements

- Go 1.22+
- libsqlite3 (`brew install sqlite` on macOS, `apt install libsqlite3-dev` on Linux)

## CLI flags

```
./workflow [flags]

  -addr       Listen address (default: :7070)
  -db         SQLite database path (default: ./workflow.db)
  -templates  Template glob (default: ./templates/*.html)
  -config     Config file path (default: ./workflow.json)
```

## Configuration (`workflow.json`)

Created automatically on first run. All settings live here — no `.env` file needed.

```json
{
  "github_token":    "",
  "anthropic_key":   "",
  "claude_model":    "claude-opus-4-6",
  "claude_base_url": "https://api.anthropic.com",
  "claude_mode":     "api",
  "pr_prompt":       "... see default below ...",
  "tiers": [
    { "key": "today",     "label": "Today",     "order": 1 },
    { "key": "this_week", "label": "This Week",  "order": 2 },
    { "key": "backlog",   "label": "Backlog",    "order": 3 }
  ],
  "work_types": [
    { "key": "pr_review",  "label": "PR Review",  "depth": "deep"    },
    { "key": "coding",     "label": "Coding",     "depth": "deep"    },
    { "key": "deployment", "label": "Deployment", "depth": "deep"    },
    { "key": "design",     "label": "Design",     "depth": "deep"    },
    { "key": "doc",        "label": "Doc",        "depth": "medium"  },
    { "key": "timeline",   "label": "Timeline",   "depth": "medium"  },
    { "key": "approval",   "label": "Approval",   "depth": "shallow" },
    { "key": "chase",      "label": "Chase",      "depth": "shallow" },
    { "key": "meeting",    "label": "Meeting",    "depth": "shallow" },
    { "key": "misc",       "label": "Misc",       "depth": "shallow" }
  ]
}
```

| Field | Required | Description |
|---|---|---|
| `github_token` | For private repos | Personal access token with `repo` scope. Get it with `gh auth token`. Without it, only public repos work (60 req/hr). |
| `anthropic_key` | For PR analysis | Your Anthropic API key. |
| `claude_model` | No | Defaults to `claude-opus-4-6`. Extended thinking auto-enabled for `claude-opus-4-*` and `claude-3-7-*` models. |
| `claude_base_url` | No | Defaults to `https://api.anthropic.com`. Override for proxies or API-compatible providers. |
| `claude_mode` | No | `"api"` (default) calls the Anthropic API directly. `"local"` runs the `claude` CLI with `--dangerously-skip-permissions` — uses your local Claude Code install, no `anthropic_key` needed. |
| `pr_prompt` | No | The prompt sent to Claude for PR analysis. Use `{{.PRURL}}` and `{{.Diff}}` as placeholders. Defaults to a structured review brief (summary, key files, potential issues, suggestions). Config is hot-reloaded — edit and save, no restart needed. |

**`depth`** controls card border colour: `deep` = purple, `medium` = amber, `shallow` = grey.

## PR Analysis

On any task with a PR URL set, click **Analyse PR** to:

1. Fetch the full diff from the GitHub API
2. Send it to Claude with a structured prompt
3. Get back a markdown-rendered brief: summary, key files, potential issues, suggestions

The result is stored in the DB — click **Re-analyse** to refresh.
