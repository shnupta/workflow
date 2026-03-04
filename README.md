# workflow

A local web app for managing work inflows as a lead/IC. Captures everything coming at you — PR reviews, deployments, docs, design discussions, chases, approvals — and routes them into a three-tier board (Today / This Week / Backlog) with a focus on what's blocking you vs what you're waiting on.

Built to reduce context switching by giving you a single place to triage and batch similar work.

## Features

- **Three-tier board** — Today, This Week, Backlog. Simple, manual, no auto-sorting.
- **Work types** — PR Review, Deployment, Doc, Design, Coding, Timeline, Approval, Chase, Meeting, Misc. Cards are colour-coded by depth (deep focus / medium / shallow).
- **Blocked on me vs them** — tasks waiting on someone else are visually dimmed so you know what you can actually act on.
- **PR analysis** — paste a GitHub PR URL, click Analyse, and Claude fetches the diff and gives you a structured review brief: summary, key files to focus on, potential issues, suggestions.
- **SQLite storage** — everything lives in a single local file, no external dependencies.

## Setup

```bash
git clone https://github.com/shnupta/workflow
cd workflow
./setup.sh
```

Then edit `.env` with your tokens and run:

```bash
source .env && ./workflow
```

Open [http://localhost:7070](http://localhost:7070).

## Requirements

- Go 1.22+
- libsqlite3 (`brew install sqlite` on macOS, `apt install libsqlite3-dev` on Linux)

## Options

```
./workflow [flags]

  -addr      Listen address (default: :7070)
  -db        SQLite database path (default: ./workflow.db)
  -templates Template glob (default: ./templates/*.html)
```

## PR Analysis

When you add a task of type **PR Review** and paste a GitHub PR URL, a button appears on the task view to analyse the PR. This:

1. Fetches the diff via the GitHub API (using your `GITHUB_TOKEN`)
2. Sends it to Claude (`ANTHROPIC_API_KEY`) with a structured review prompt
3. Returns: summary, key files to focus on, potential issues, suggestions

Diffs are capped at 500KB. The summary is stored in the DB so you only need to fetch it once.

## Work type depth

Cards have a left border colour indicating how much focus the task requires:

| Colour | Depth | Types |
|--------|-------|-------|
| Purple | Deep  | PR Review, Coding, Design, Deployment |
| Amber  | Medium | Doc, Timeline |
| Grey   | Shallow | Approval, Chase, Meeting, Misc |

Use this to batch your day: deep work in focus blocks, shallow work in a single batched slot.
