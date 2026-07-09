<div align="center">
  <img src=".github/assets/togo-mark.svg" alt="togo" height="64" />
  <h1>togo-framework/live</h1>
  <p>
    <a href="https://to-go.dev/marketplace"><img src="https://img.shields.io/badge/marketplace-to--go.dev-1FC7DC" alt="marketplace" /></a>
    <a href="https://pkg.go.dev/github.com/togo-framework/live"><img src="https://pkg.go.dev/badge/github.com/togo-framework/live.svg" alt="pkg.go.dev" /></a>
    <img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" />
  </p>
  <p><strong>Bring AI agents to life in <a href="https://to-go.dev">togo</a> — push a prompt, an agent answers, the reply streams back over any channel.</strong></p>
</div>

## Install

```bash
togo install togo-framework/live
```

## What it is

`live` is an **AI-agent bridge**. You push a prompt — a chat message, a row in a
table, or a third-party message like WhatsApp — and a **live agent** (Claude or
`omni`, typically running inside its own [Coder](https://to-go.dev/plugins/coder)
workspace) answers it. The reply is stored, streamed live over a WebSocket, and
delivered back out over the channel the conversation came in on.

It generalizes the [`autopilot`](https://to-go.dev/plugins/autopilot) loop from
*"issue → code → PR"* into *"prompt → reply → any channel"*, and reuses the same
`impl`/`exec` provider slots — so **omnigent inside coder** composes for free.

### Data model

| Table | Meaning |
|---|---|
| `live_agents` | a persona + backend (`echo`/`claude`/`omni`) + a scoped MCP token |
| `live_conversations` | one thread with an agent on one channel (`chat`/`whatsapp`/…) |
| `live_messages` | one turn — `role=user` (a prompt) or `role=agent` (a reply) |

Inserting a `role=user` message **is** "pushing a prompt". `status=pending` → the
agent answers it → a `role=agent` reply appears and is delivered.

## Two runtime modes (one API), via `LIVE_MODE`

**`agent`** (default) — *bring the agent to life.* A long-running `claude`/`omni`
inside a Coder workspace polls its inbox and pushes replies through the **live MCP
bridge**. Its loop is literally *"call `live_inbox` → think → call `live_reply`"*.

```
claude/omni (in Coder ws) ──MCP──> live-mcp ──HTTPS──> /api/live/inbox + /reply ──> app DB ──> channel
```

**`server`** — *zero always-on workspace.* An in-app runner claims pending prompts
and invokes the agent one-shot via a `Responder` (`echo`/`claude`/`cmd`), replaying
the transcript for context. Great for demos, tests, and simple deployments.

```bash
LIVE_MODE=server LIVE_RESPONDER=claude   # or echo (deterministic) / cmd (any binary)
```

## Quick start

```bash
# 1. run an app with live installed (server mode + a seeded demo agent)
LIVE_MODE=server LIVE_RESPONDER=echo LIVE_SEED_AGENT=Ada togo serve

# 2. open the built-in chat board
open http://localhost:8080/live

# 3. or drive it over the API
curl -sX POST localhost:8080/api/live/conversations/$CONV/messages \
  -H content-type:application/json -d '{"body":"hello agent"}'
```

See [`example/`](example) for a runnable app + a Playwright end-to-end test.

## REST API (`/api/live`)

| Method & path | Purpose |
|---|---|
| `POST /agents` | register an agent → returns its bearer token **once** |
| `GET /agents` | list agents |
| `POST /conversations` · `GET /conversations` · `GET /conversations/{id}` | manage threads |
| `GET /conversations/{id}/messages` | transcript |
| `POST /conversations/{id}/messages` | **push a prompt** (`{"body":"…"}`) |
| `GET /ws` | live event stream (new message, typing) |
| `GET /inbox` · `POST /reply` · `POST /typing` · `GET /history` | **agent surface** — `Authorization: Bearer <agent token>` |

## The MCP bridge (`cmd/live-mcp`)

The binary an agent runs inside its workspace to become live. Tools: `live_inbox`,
`live_history`, `live_typing`, `live_reply`. Configure it in the workspace's
`.mcp.json`:

```json
{ "mcpServers": { "live": {
  "command": "live-mcp",
  "env": { "LIVE_API_URL": "https://your-app.example", "LIVE_AGENT_TOKEN": "lat_…" }
} } }
```

## Channels

`chat` and `table` are in-app (stored + streamed over the WS). Third-party channels
are driver plugins that implement `live.Channel` and `RegisterChannel(...)` — the
same shape as [`notifications`](https://to-go.dev/plugins/notifications) channels:

- [`live-whatsapp`](https://to-go.dev/plugins/live-whatsapp) — WhatsApp Cloud API (in + out)
- [`live-notify`](https://to-go.dev/plugins/live-notify) — **one bridge to the whole `notifications` system**: a conversation on the `slack`/`discord`/`notify` channel delivers through the existing `notifications-slack`/`-discord`/`-webpush`/`-fcm`/`-pusher` drivers, no per-provider code. Set `LIVE_NOTIFY_CHANNELS=slack,discord` for the fan-out `notify` channel.

## Deploying a live agent (agent mode)

A `live-agent` Coder workspace (or any box / Docker) that installs `claude`/`omni`,
self-registers (`POST /api/live/agents`), writes the returned token into its
`.mcp.json` for `live-mcp`, then polls and answers on its own. Turn-key scripts,
`docker-compose`, and a Coder template are in [`deploy/`](deploy) — `bash
deploy/scripts/agent-boot.sh && bash deploy/scripts/agent-loop.sh`.

<!-- togo-sponsors -->
---
<div align="center"><sub>💎 Premium sponsors — <b>ID8 Media</b> · <b>One Studio</b></sub></div>
