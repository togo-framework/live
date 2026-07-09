# live — usage

`live` turns a togo app into an **AI-agent bridge**: push a prompt (a chat
message, a table row, or a third-party message like WhatsApp) and a **live agent**
(Claude/omni) answers it. The reply is stored, streamed live over a WebSocket, and
delivered back out on the channel the conversation came in on.

Install and blank-import as usual:

```bash
togo install togo-framework/live
```

The plugin self-registers, mounts under `/api/live/*`, and serves a self-contained
chat board at `/live`.

## Data model

| Table | Struct | Meaning |
|---|---|---|
| `live_agents` | `Agent` | persona + backend `provider` (`echo`/`claude`/`omni`), optional Coder `workspace`, and a scoped MCP bearer token |
| `live_conversations` | `Conversation` | one thread with an agent on one `channel`; `external_ref` holds the provider thread id (e.g. a WhatsApp msisdn) |
| `live_messages` | `Message` | one turn — `role=user` (a prompt) or `role=agent` (a reply); a prompt starts `status=pending` and flips to `done` when answered |

Inserting a `role=user` message **is** "pushing a prompt". The agent answers it and
a `role=agent` reply is created and delivered.

## Two runtime modes (`LIVE_MODE`)

Both modes share the same data model and REST API.

**`agent`** (default) — *bring the agent to life.* A long-running `claude`/`omni`
inside a Coder workspace polls its inbox and pushes replies through the **live MCP
bridge** (`cmd/live-mcp` → the token-guarded `/api/live/inbox` + `/reply`). The app
never runs the agent; the agent pulls its own work.

**`server`** — *zero always-on workspace.* An in-app runner (`newRunner`) claims
pending prompts every `LIVE_POLL_SECONDS` and invokes the agent one-shot via a
`Responder`, replaying the prior transcript as context. Great for demos, tests, and
simple deployments.

### Environment variables

| Var | Default | Meaning |
|---|---|---|
| `LIVE_MODE` | `agent` | `agent` (MCP bridge) or `server` (in-app runner) |
| `LIVE_RESPONDER` | `echo` | server mode only: `echo` (deterministic), `claude` (shells `claude -p`), `cmd` (any binary via `LIVE_RESPONDER_CMD`) |
| `LIVE_SEED_AGENT` | — | if set, seed one agent + an open `chat` conversation when the DB is empty (demos/e2e); logs the agent token |
| `LIVE_POLL_SECONDS` | `3` | server mode runner poll interval (min 2) |

Other env read by the code: `LIVE_SEED_PERSONA`, `LIVE_SEED_PROVIDER` (seed
defaults); `LIVE_AGENT_ID`, `LIVE_RUNNER_NAME` (scope/name the runner);
`LIVE_CLAUDE_BIN`, `LIVE_RESPONDER_CMD` (responder binaries); `LIVE_INSERT_HOOK`
(kernel hook name whose `*.created` payload — `{conversation_id, body}` — is ingested
as a prompt); `LIVE_NOTIFY_CHANNELS` (fan-out for the `live-notify` bridge).

## REST API (`/api/live`)

### Human / app surface

| Method & path | Purpose |
|---|---|
| `POST /agents` | register an agent → returns its bearer token **once** (`{name, persona?, provider?, workspace?}`) |
| `GET /agents` | list agents |
| `POST /conversations` | open a thread (`{agent_id, channel?, external_ref?, title?}`; `channel` defaults to `chat`) |
| `GET /conversations` | list threads (optional `?agent_id=`) |
| `GET /conversations/{id}` | fetch a thread |
| `GET /conversations/{id}/messages` | full transcript |
| `POST /conversations/{id}/messages` | **push a prompt** (`{body, meta?}`) |
| `GET /ws` | live event stream (`message.created`, `typing`) |

### Agent surface (token-guarded)

Authenticate with `Authorization: Bearer <agent token>` (or `X-Live-Token`). This is
what the MCP bridge calls.

| Method & path | Purpose |
|---|---|
| `GET /inbox` | the calling agent's pending prompts |
| `POST /reply` | post a reply (`{conversation_id, body}`); resolves + closes the oldest open prompt, streams + delivers it |
| `POST /typing` | broadcast a typing indicator (`{conversation_id}`) |
| `GET /history` | full transcript of a conversation the agent owns (`?conversation_id=`) |

`/reply`, `/typing`, and `/history` all verify the conversation belongs to the
authenticated agent (`conv.AgentID == agent.ID`) and 403 otherwise.

## The MCP bridge (`cmd/live-mcp`)

The binary an agent runs inside its workspace to become live. Tools:

| Tool | Wraps |
|---|---|
| `live_inbox` | `GET /inbox` — list pending prompts |
| `live_history` | `GET /history` — rebuild conversation context |
| `live_typing` | `POST /typing` — show a typing indicator |
| `live_reply` | `POST /reply` — answer a prompt |

Configure it in the workspace's `.mcp.json`:

```json
{ "mcpServers": { "live": {
  "command": "live-mcp",
  "env": { "LIVE_API_URL": "https://your-app.example", "LIVE_AGENT_TOKEN": "lat_…" }
} } }
```

The loop is literally *"call `live_inbox` → think → call `live_reply`"*.

## Channels

A conversation's `channel` decides egress. `chat` and `table` are in-app (stored and
streamed over the WS hub). Third-party channels are driver plugins that implement
`live.Channel` (`Name()` + `Deliver(ctx, conv, msg)`) and register via
`RegisterChannel(name, factory)` — the same shape as `notifications` channels; an app
activates one by blank-importing it.

- [`live-whatsapp`](https://to-go.dev/plugins/live-whatsapp) — WhatsApp Cloud API (in + out)
- [`live-notify`](https://to-go.dev/plugins/live-notify) — one bridge to the whole `notifications` system: a conversation on the `slack`/`discord`/`notify` channel delivers through the existing notifications drivers. Set `LIVE_NOTIFY_CHANNELS=slack,discord` for the fan-out `notify` channel.

## Quick start (server mode + echo)

```bash
LIVE_MODE=server LIVE_RESPONDER=echo LIVE_SEED_AGENT=Ada togo serve
open http://localhost:8080/live

# or drive it over the API
curl -sX POST localhost:8080/api/live/conversations/$CONV/messages \
  -H content-type:application/json -d '{"body":"hello agent"}'
```

See [`example/`](../example) for a runnable app + a Playwright end-to-end test, and
[`deploy/`](../deploy) for bringing an agent to life in `agent` mode.
