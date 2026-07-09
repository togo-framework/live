# Deploying a live agent

Bring a live agent to life on any host — it self-registers with your togo app and
answers prompts through the `live` MCP bridge. Three ways, same two scripts:

| | Use it for |
|---|---|
| **`scripts/`** (`agent-boot.sh` + `agent-loop.sh`) | any Linux box / existing workspace |
| **`docker-compose.yml`** | deploy anywhere with Docker; scale to N agents |
| **`coder-template/`** | one disposable Coder workspace per agent (the exec provider target) |

## How it works

```
agent-boot.sh   → installs claude + live-mcp, POST /api/live/agents (gets a token),
                  writes .mcp.json { mcpServers.live → live-mcp + token }
agent-loop.sh   → every few seconds: claude -p "drain your live inbox" with the live
                  MCP attached → live_inbox → live_history → live_reply
```

The app never runs the agent; the agent brings itself alive and pulls work. This is
`LIVE_MODE=agent`. (For a zero-workspace setup, run the app in `LIVE_MODE=server`
instead and skip all of this.)

## Quick start — Docker

```bash
export LIVE_API_URL=https://your-app.example
export ANTHROPIC_API_KEY=sk-ant-...        # or bake `claude login` into the image
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml up -d --scale agent=3   # 3 agents
```

## Quick start — any box

```bash
export LIVE_API_URL=https://your-app.example
export LIVE_AGENT_NAME=Ada LIVE_AGENT_PERSONA="a concise support agent"
bash deploy/scripts/agent-boot.sh
bash deploy/scripts/agent-loop.sh
```

## Quick start — Coder (one workspace per agent)

```bash
coder templates push live-agent -d deploy/coder-template --var live_api_url=https://your-app.example
coder create ada --template live-agent
```

Use an image that pre-ships `go`, `node`, `claude` (logged in), optionally `omni`,
and this `deploy/scripts` dir at `/opt/live` for a fast cold start. The
`togo-framework/coder` exec provider points `CODER_TEMPLATE` at this template so an
app can provision agents on demand.

## Notes

- **Secrets:** provide `ANTHROPIC_API_KEY` via env/Coder parameter, or pre-authenticate
  `claude login` in the base image. Never commit keys or the returned agent token.
- **omni instead of claude:** set `LIVE_AGENT_PROVIDER=omni` and `LIVE_AGENT_BIN=omni`.
- The token from registration is shown once — `agent-boot.sh` stores it in
  `~/.live-agent.env`; treat that file as a secret.
