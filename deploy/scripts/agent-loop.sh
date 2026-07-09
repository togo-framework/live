#!/usr/bin/env bash
# agent-loop.sh — the loop that keeps a live agent alive.
#
# Every LIVE_POLL_SECONDS it runs claude headless in the work dir (where
# agent-boot.sh wrote .mcp.json), telling it to drain its live inbox: read each
# pending prompt, pull context, and post a reply — all through the live_* MCP
# tools. Requires ~/.live-agent.env from agent-boot.sh.
#
# Optional env:
#   LIVE_POLL_SECONDS  seconds between sweeps (default: 5)
#   LIVE_AGENT_BIN     claude | omni  (default: claude)
set -euo pipefail

# shellcheck source=/dev/null
source "$HOME/.live-agent.env"
export PATH="$PATH:$(go env GOPATH 2>/dev/null)/bin"
cd "$LIVE_WORK_DIR"

POLL="${LIVE_POLL_SECONDS:-5}"
BIN="${LIVE_AGENT_BIN:-claude}"

read -r -d '' PROMPT <<'EOP' || true
You are a live agent. Use ONLY the `live` MCP tools.
1. Call live_inbox to list pending prompts addressed to you.
2. If it is empty, do nothing and stop.
3. For each pending prompt: call live_typing(conversation_id), then live_history(conversation_id)
   for context, decide a concise helpful answer, and call live_reply(conversation_id, body).
Answer every pending prompt, then stop. Do not invent conversations.
EOP

echo "==> $LIVE_AGENT_NAME is live — polling every ${POLL}s (Ctrl-C to stop)"
while true; do
  "$BIN" -p "$PROMPT" --permission-mode acceptEdits >/dev/null 2>&1 || echo "(sweep error — retrying)"
  sleep "$POLL"
done
