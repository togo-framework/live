// Package live turns togo into an AI-agent bridge: push a prompt (a row in a
// table, a chat message, or a third-party message like WhatsApp) and a live agent
// — Claude/omni running in its own Coder workspace — answers it, with the reply
// streamed back over the same channel.
//
// Two runtime modes, selected by LIVE_MODE, share one data model and API:
//
//   - agent-driven (default): a long-running claude/omni inside a Coder workspace
//     polls its inbox and pushes replies through the live MCP bridge
//     (cmd/live-mcp → token-guarded /api/live/inbox + /reply). "The agent reads
//     the table over MCP and pushes responses."
//   - server-driven: an in-app runner claims pending prompts and invokes the
//     agent one-shot via a Responder (echo/claude/slot), replaying the transcript
//     for context. Zero always-on workspace; reuses the togo impl/exec slots
//     (omnigent + coder) when selected.
//
// Mounted under /api/live/*; a self-contained chat board is served at /live.
package live

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"
)

// Runtime modes.
const (
	ModeAgent  = "agent"  // agent-driven (MCP inbox/reply)
	ModeServer = "server" // server-driven (in-app runner + Responder)
)

var errNoDB = errors.New("live: no database configured")

func init() {
	// PriorityLate+20: mount after core plugins + auth middleware.
	togo.RegisterProviderFunc("live", togo.PriorityLate+20, func(k *togo.Kernel) error {
		if k.Router == nil {
			return nil
		}
		s := &server{
			k:        k,
			hub:      newHub(),
			mode:     env("LIVE_MODE", ModeAgent),
			channels: buildChannels(k),
		}
		if err := s.migrate(context.Background()); err != nil {
			if k.Log != nil {
				k.Log.Error("live: migrate failed", "err", err)
			}
			return nil // don't take the app down; the board just won't work
		}
		s.mount(k.Router)
		k.Set("live", &Service{s}) // published for channel drivers / app code

		// Bridge: when the app is generator-first and exposes a `Message`-like
		// resource, an insert fires "<hook>.created" on the kernel bus. Apps can
		// point LIVE_INSERT_HOOK at that event to have live ingest rows without a
		// direct API call. Off unless configured.
		if hook := os.Getenv("LIVE_INSERT_HOOK"); hook != "" && k.Hooks != nil {
			k.Hooks.On(hook, 50, s.onInsertHook)
		}

		// Optional demo/e2e seed: create one agent + an open chat if the DB is empty.
		if name := os.Getenv("LIVE_SEED_AGENT"); name != "" {
			s.seed(context.Background(), name)
		}

		if s.mode == ModeServer {
			s.responder = newResponder(k)
			r := newRunner(s)
			go r.loop(context.Background())
			if k.Log != nil {
				k.Log.Info("live: server-driven runner started", "responder", env("LIVE_RESPONDER", "echo"))
			}
		} else if k.Log != nil {
			k.Log.Info("live: agent-driven mode (agents poll /api/live/inbox via the MCP bridge)")
		}
		return nil
	})
}

type server struct {
	k    *togo.Kernel
	mode string
	// testDB, when set, bypasses the kernel so the store/loop are unit-testable
	// against an in-memory SQLite DB (uses "?" placeholders).
	testDB   *sql.DB
	hub      *hub
	channels map[string]Channel
	responder Responder
}

func (s *server) mount(r chi.Router) {
	r.Route("/api/live", func(r chi.Router) {
		// Human/app surface.
		r.Get("/agents", s.listAgentsHandler)
		r.Post("/agents", s.registerAgent)
		r.Get("/conversations", s.listConversationsHandler)
		r.Post("/conversations", s.createConversationHandler)
		r.Get("/conversations/{id}", s.getConversationHandler)
		r.Get("/conversations/{id}/messages", s.listMessagesHandler)
		r.Post("/conversations/{id}/messages", s.postPrompt) // push a prompt
		r.Get("/ws", s.serveWS)                              // live chat stream

		// Agent surface (Bearer <agent token>) — used by the MCP bridge.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAgentToken)
			r.Get("/inbox", s.inbox)
			r.Post("/reply", s.reply)
			r.Post("/typing", s.typing)
			r.Get("/history", s.history)
		})
	})

	// Self-contained chat board (served at the root so the shell loads without a
	// session; its data calls hit /api/live above).
	r.Get("/live", s.serveBoard)
}

// ---- shared reply / prompt plumbing (used by loop.go, api.go, channels) ----

// receivePrompt records an inbound user prompt and notifies subscribers. In
// server mode the runner will claim it; in agent mode it appears in the agent's
// MCP inbox.
func (s *server) receivePrompt(ctx context.Context, conv Conversation, body, meta string) (Message, error) {
	m := Message{
		ID: genID(), ConversationID: conv.ID, Role: RoleUser, Body: body,
		Status: MsgPending, Meta: meta, CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if err := s.createMessage(ctx, m); err != nil {
		return Message{}, err
	}
	s.emitMessage("message.created", m)
	return m, nil
}

// postReply stores an agent reply, marks the triggering prompt done, streams the
// reply over the hub, and delivers it to the conversation's external channel (if
// any). This is the single path every reply flows through, whether it came from
// the server-driven Responder or an agent's /reply call.
func (s *server) postReply(ctx context.Context, conv Conversation, body, promptID string) (Message, error) {
	m := Message{
		ID: genID(), ConversationID: conv.ID, Role: RoleAgent, Body: body,
		Status: MsgDone, CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if err := s.createMessage(ctx, m); err != nil {
		return Message{}, err
	}
	if promptID != "" {
		s.setMessageStatus(ctx, promptID, MsgDone)
	}
	s.emitMessage("message.created", m)
	// External egress (whatsapp/slack/discord); table/chat are in-app only.
	if ch, ok := s.channels[conv.Channel]; ok {
		if err := ch.Deliver(ctx, conv, m); err != nil && s.k != nil && s.k.Log != nil {
			s.k.Log.Error("live: channel deliver failed", "channel", conv.Channel, "err", err)
		}
	}
	return m, nil
}

// onInsertHook adapts a generated-resource "created" hook payload into a prompt.
// Payload shape is app-specific; we accept a map with conversation_id + body, or
// a Message-like struct marshalled to JSON.
func (s *server) onInsertHook(ctx context.Context, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var row struct {
		ConversationID string `json:"conversation_id"`
		Body           string `json:"body"`
		Role           string `json:"role"`
	}
	if err := json.Unmarshal(b, &row); err != nil || row.ConversationID == "" || row.Role == RoleAgent {
		return nil
	}
	conv, ok := s.getConversation(ctx, row.ConversationID)
	if !ok {
		return nil
	}
	_, _ = s.receivePrompt(ctx, conv, row.Body, "")
	return nil
}

// seed creates a single agent + open chat conversation when the DB has no agents
// yet, for demos and end-to-end tests. Idempotent. Logs the agent token so an
// external MCP bridge can be pointed at it.
func (s *server) seed(ctx context.Context, name string) {
	if len(s.listAgents(ctx)) > 0 {
		return
	}
	a := Agent{
		ID: genID(), Name: name, Persona: env("LIVE_SEED_PERSONA", "a helpful live agent"),
		Provider: env("LIVE_SEED_PROVIDER", "echo"), Status: "idle", Token: genToken(),
		CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if err := s.createAgent(ctx, a); err != nil {
		return
	}
	c := Conversation{
		ID: genID(), AgentID: a.ID, Channel: "chat", Title: "Demo chat",
		Status: "open", CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	_ = s.createConversation(ctx, c)
	if s.k != nil && s.k.Log != nil {
		s.k.Log.Info("live: seeded demo agent", "agent", a.Name, "agent_id", a.ID, "conversation_id", c.ID, "token", a.Token)
	}
}

// ---- helpers ----

func (s *server) db(ctx context.Context) (*sql.DB, func(int) string) {
	if s.testDB != nil {
		return s.testDB, func(int) string { return "?" }
	}
	if s.k == nil {
		return nil, func(int) string { return "?" }
	}
	db, _ := s.k.SQL(ctx)
	return db, s.k.Dialect().Placeholder
}

func genID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// genToken returns an unguessable agent bearer token.
func genToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "lat_" + hex.EncodeToString(b)
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339) }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
