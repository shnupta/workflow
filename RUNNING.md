# Running workflow locally (server / dev environment)

## Server setup

- Go: `/usr/local/go/bin/go` — always `export PATH=$PATH:/usr/local/go/bin` first
- Claude CLI: `/root/.local/bin/claude` (version 2.1.63, Claude Code)
- Claude auth: already authenticated, persists in `~/.claude/`
- Git: push access to `https://github.com/shnupta/workflow.git` configured

## Building

```bash
export PATH=$PATH:/usr/local/go/bin
cd /root/.nox/workspace/workflow
go build ./...                    # verify
go build -o workflow-server ./cmd/workflow/  # produce binary
```

## Running on the server (for testing with Playwright)

```bash
cd /root/.nox/workspace/workflow
nohup ./workflow-server \
  -addr :7070 \
  -db ./test.db \
  -templates './templates/*.html' \
  -config ./workflow.json \
  > /tmp/workflow.log 2>&1 &
echo $! > /tmp/workflow.pid
```

Stop: `kill $(cat /tmp/workflow.pid)`
Logs: `tail -f /tmp/workflow.log`
Health: `curl -s -o /dev/null -w "%{http_code}" http://localhost:7070/`

## workflow.json (server-side, gitignored)

```json
{
  "claude_bin": "/root/.local/bin/claude",
  "tiers": [...],
  "work_types": [...]
}
```

## Root / permission workaround (SERVER ONLY)

Claude CLI refuses `--dangerously-skip-permissions` when running as root.
On this server: set `CLAUDE_ALLOW_ROOT=1` env var in the agent subprocess.
This is injected in `claude_local.go` only when `WORKFLOW_DEV_ROOT=1` is set in the server environment.
**Do not enable this in production / on Casey's machine — it's a server-only workaround.**

## Playwright testing

Playwright MCP is available. Navigate to `http://localhost:7070` after starting the server.

## Casey's machine (production)

- Casey runs `./workflow` directly on his Mac
- Config: `~/workflow.json` with `claude_bin` pointing to his local Claude CLI
- `workflow.json` is gitignored — never committed
- Casey pulls + rebuilds after each push: `git pull && go build ./cmd/workflow/ && ./workflow`
