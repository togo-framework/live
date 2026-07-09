#!/usr/bin/env bash
# agent-boot.sh — bring a live agent to life on any host/workspace.
#
# It (1) ensures claude + the live-mcp bridge are installed, (2) self-registers
# the agent with your togo app (POST /api/live/agents) to obtain a scoped token,
# (3) writes an .mcp.json so the running claude/omni can call the live_* tools,
# and (4) persists the token to ~/.live-agent.env for agent-loop.sh.
#
# Required env:
#   LIVE_API_URL        base URL of your togo app, e.g. https://app.example.com
# Optional env:
#   LIVE_AGENT_NAME     display name (default: hostname)
#   LIVE_AGENT_PERSONA  system prompt / character
#   LIVE_AGENT_PROVIDER claude | omni  (default: claude)
#   LIVE_WORK_DIR       where .mcp.json lives + where the agent runs (default: ~/live-agent)
set -euo pipefail

: "${LIVE_API_URL:?set LIVE_API_URL to your togo app base URL}"
LIVE_AGENT_NAME="${LIVE_AGENT_NAME:-$(hostname)}"
LIVE_AGENT_PROVIDER="${LIVE_AGENT_PROVIDER:-claude}"
LIVE_WORK_DIR="${LIVE_WORK_DIR:-$HOME/live-agent}"
mkdir -p "$LIVE_WORK_DIR"

echo "==> ensuring toolchain (claude, live-mcp)…"
command -v claude   >/dev/null 2>&1 || npm i -g @anthropic-ai/claude-code
command -v live-mcp >/dev/null 2>&1 || go install github.com/togo-framework/live/cmd/live-mcp@latest
export PATH="$PATH:$(go env GOPATH)/bin"
if [ "$LIVE_AGENT_PROVIDER" = "omni" ]; then command -v omni >/dev/null 2>&1 || echo "!! omni not found — install it or use provider=claude"; fi

echo "==> registering agent '$LIVE_AGENT_NAME' with $LIVE_API_URL…"
resp="$(curl -fsS -X POST "$LIVE_API_URL/api/live/agents" \
  -H 'content-type: application/json' \
  -d "$(jq -n --arg n "$LIVE_AGENT_NAME" --arg p "${LIVE_AGENT_PERSONA:-}" --arg pr "$LIVE_AGENT_PROVIDER" --arg w "$(hostname)" \
        '{name:$n, persona:$p, provider:$pr, workspace:$w}')")"
TOKEN="$(echo "$resp" | jq -r '.token')"
AGENT_ID="$(echo "$resp" | jq -r '.id')"
[ -n "$TOKEN" ] && [ "$TOKEN" != "null" ] || { echo "registration failed: $resp"; exit 1; }

echo "==> writing $LIVE_WORK_DIR/.mcp.json"
jq -n --arg url "$LIVE_API_URL" --arg tok "$TOKEN" '{
  mcpServers: { live: { command: "live-mcp", env: { LIVE_API_URL: $url, LIVE_AGENT_TOKEN: $tok } } }
}' > "$LIVE_WORK_DIR/.mcp.json"

cat > "$HOME/.live-agent.env" <<EOF
export LIVE_API_URL="$LIVE_API_URL"
export LIVE_AGENT_TOKEN="$TOKEN"
export LIVE_AGENT_ID="$AGENT_ID"
export LIVE_AGENT_NAME="$LIVE_AGENT_NAME"
export LIVE_WORK_DIR="$LIVE_WORK_DIR"
EOF

echo "==> agent '$LIVE_AGENT_NAME' ($AGENT_ID) is registered."
echo "    start answering with:  bash agent-loop.sh"
