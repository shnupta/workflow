# workflow

A local web app for managing work inflows as a lead/IC. Captures everything coming at you — PR reviews, deployments, docs, design discussions, chases, approvals — and routes them into a three-tier board (Today / This Week / Backlog) with a focus on what's blocking you vs what you're waiting on.

Built to reduce context switching by giving you a single place to triage and batch similar work.

## Features

- **Three-tier board** — Today, This Week, Backlog. Simple, manual, no auto-sorting.
- **Work types** — PR Review, Deployment, Doc, Design, Coding, Timeline, Approval, Chase, Meeting, Misc. Cards are colour-coded by depth (deep focus / medium / shallow).
- **Blocked on me vs them** — tasks waiting on someone else are visually dimmed so you know what you can actually act on.
- **PR analysis** — paste a GitHub PR URL on any task, click Analyse, and Claude fetches the full diff and gives you a structured review brief: summary, key files to focus on, potential issues, suggestions. Output rendered as markdown.
- **Configurable** — work types, tiers, and depth are all defined in `workflow.json`, auto-created with sensible defaults on first run.
- **SQLite storage** — everything lives in a single local file, no external dependencies.

## Setup

```bash
git clone https://github.com/shnupta/workflow
cd workflow
./setup.sh
```

Edit `.env` with your tokens. In fish shell:

```fish
set -x GITHUB_TOKEN (gh auth token)
set -x ANTHROPIC_API_KEY sk-ant-...
./workflow
```

Or in bash/zsh:

```bash
source .env && ./workflow
```

Open [http://localhost:7070](http://localhost:7070).

On first run, `workflow.json` is created in the working directory with default work types and tiers. Edit it to customise.

## Requirements

- Go 1.22+
- libsqlite3 (`brew install sqlite` on macOS, `apt install libsqlite3-dev` on Linux)

## Options

```
./workflow [flags]

  -addr       Listen address (default: :7070)
  -db         SQLite database path (default: ./workflow.db)
  -templates  Template glob (default: ./templates/*.html)
  -config     Config file path (default: ./workflow.json, created if absent)
```

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `GITHUB_TOKEN` | For private repos | Personal access token with `repo` scope. Without it, only public repos work (60 req/hr limit). Get it with `gh auth token`. |
| `ANTHROPIC_API_KEY` | For PR analysis | Your Anthropic API key. |
| `CLAUDE_MODEL` | No | Model to use for PR analysis (default: `claude-opus-4-6`). Thinking is auto-enabled for `claude-opus-4-*` and `claude-3-7-*` models. |
| `CLAUDE_BASE_URL` | No | Override the Claude API base URL (default: `https://api.anthropic.com`). Useful for proxies or API-compatible providers. |

## Configuration (`workflow.json`)

Auto-created on first run. Edit to customise work types and tiers:

```json
{
  "tiers": [
    { "key": "today",     "label": "Today",     "order": 1 },
    { "key": "this_week", "label": "This Week",  "order": 2 },
    { "key": "backlog",   "label": "Backlog",    "order": 3 }
  ],
  "work_types": [
    { "key": "pr_review",   "label": "PR Review",   "depth": "deep"    },
    { "key": "coding",      "label": "Coding",      "depth": "deep"    },
    { "key": "deployment",  "label": "Deployment",  "depth": "deep"    },
    { "key": "design",      "label": "Design",      "depth": "deep"    },
    { "key": "doc",         "label": "Doc",         "depth": "medium"  },
    { "key": "timeline",    "label": "Timeline",    "depth": "medium"  },
    { "key": "approval",    "label": "Approval",    "depth": "shallow" },
    { "key": "chase",       "label": "Chase",       "depth": "shallow" },
    { "key": "meeting",     "label": "Meeting",     "depth": "shallow" },
    { "key": "misc",        "label": "Misc",        "depth": "shallow" }
  ]
}
```

`depth` controls the card's left border colour: `deep` = purple, `medium` = amber, `shallow` = grey. Use this to batch your day — focus blocks for deep work, a single slot for shallow.

## PR Analysis

When a task has a PR URL set, the task view shows an **Analyse PR** button. This:

1. Parses the owner/repo/number from the URL
2. Fetches the full diff from the GitHub API (`Accept: application/vnd.github.v3.diff`)
3. Sends it to Claude with a structured prompt
4. Returns a markdown-rendered brief: summary, key files, potential issues, suggestions

The summary is stored in the DB — click **Re-analyse** to refresh it.
