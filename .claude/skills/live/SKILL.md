---
name: live
description: Bring AI agents to life in a togo app — push a prompt (chat/table/WhatsApp) and a live agent (Claude/omni) answers over any channel, via the live plugin's agent-driven MCP bridge or in-app server runner.
---

# togo live

Use this skill to wire the prompt→agent→reply loop with the `live` plugin. Mounted
under `/api/live/*`; a chat board is served at `/live`.

## Register an agent
```bash
curl -sX POST localhost:8080/api/live/agents \
  -H content-type:application/json \
  -d '{"name":"Ada","persona":"a concise support agent","provider":"claude"}'
# → returns the agent incl. its bearer token ONCE (lat_…). Store it as a secret.
```

## Open a conversation and push a prompt
```bash
curl -sX POST localhost:8080/api/live/conversations \
  -H content-type:application/json -d '{"agent_id":"'$AGENT'","channel":"chat"}'
# then push a prompt (a role=user message) into it:
curl -sX POST localhost:8080/api/live/conversations/$CONV/messages \
  -H content-type:application/json -d '{"body":"hello agent"}'
```

## Run in server mode (echo, for a demo/test)
```bash
LIVE_MODE=server LIVE_RESPONDER=echo LIVE_SEED_AGENT=Ada togo serve
open http://localhost:8080/live
```
The in-app runner claims the pending prompt and answers via the `Responder`. Swap
`LIVE_RESPONDER=claude` (shells `claude -p`) or `cmd` (`LIVE_RESPONDER_CMD=…`) for a
real LLM. Tune `LIVE_POLL_SECONDS`.

## Deploy an agent (agent mode, the default)
```bash
export LIVE_API_URL=https://your-app.example
bash deploy/scripts/agent-boot.sh   # installs claude + live-mcp, self-registers, writes .mcp.json
bash deploy/scripts/agent-loop.sh   # drains its inbox: live_inbox → live_history → live_reply
```
Or `docker compose -f deploy/docker-compose.yml up -d` (scale to N), or the Coder
template. The agent talks to `/api/live/inbox` + `/reply` over the `live-mcp` bridge.

## Notes
- Channels: `chat`/`table` are in-app (streamed over `/api/live/ws`); external egress
  is via driver plugins — `live-whatsapp`, and `live-notify` for slack/discord/push.
- Agent tokens are shown once — never commit or log them; agent endpoints validate
  conversation ownership.
- Point `LIVE_INSERT_HOOK` at a resource's `*.created` event to ingest table rows as
  prompts without an API call.
