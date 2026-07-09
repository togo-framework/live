package live

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ---- context ----

type ctxKey int

const agentCtxKey ctxKey = 0

func agentFromCtx(ctx context.Context) (Agent, bool) {
	a, ok := ctx.Value(agentCtxKey).(Agent)
	return a, ok
}

// requireAgentToken authenticates the MCP bridge via "Authorization: Bearer
// <agent token>" and puts the Agent on the request context.
func (s *server) requireAgentToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if tok == "" {
			tok = r.Header.Get("X-Live-Token")
		}
		agent, ok := s.getAgentByToken(r.Context(), tok)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		ctx := context.WithValue(r.Context(), agentCtxKey, agent)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// ---- agents ----

func (s *server) listAgentsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.listAgents(r.Context())})
}

// registerAgent creates an agent and returns its bearer token ONCE. The
// live-agent Coder template calls this on boot to self-register, then configures
// its MCP bridge with the returned token.
func (s *server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		Persona   string `json:"persona"`
		Provider  string `json:"provider"`
		Workspace string `json:"workspace"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if in.Provider == "" {
		in.Provider = "echo"
	}
	a := Agent{
		ID: genID(), Name: in.Name, Persona: in.Persona, Provider: in.Provider,
		Workspace: in.Workspace, Status: "idle", Token: genToken(),
		CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if err := s.createAgent(r.Context(), a); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a) // includes token exactly once
}

// ---- conversations ----

func (s *server) listConversationsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": s.listConversations(r.Context(), r.URL.Query().Get("agent_id")),
	})
}

func (s *server) createConversationHandler(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID     string `json:"agent_id"`
		Channel     string `json:"channel"`
		ExternalRef string `json:"external_ref"`
		Title       string `json:"title"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.AgentID) == "" {
		writeErr(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if _, ok := s.getAgent(r.Context(), in.AgentID); !ok {
		writeErr(w, http.StatusBadRequest, "unknown agent_id")
		return
	}
	if in.Channel == "" {
		in.Channel = "chat"
	}
	c := Conversation{
		ID: genID(), AgentID: in.AgentID, Channel: in.Channel, ExternalRef: in.ExternalRef,
		Title: in.Title, Status: "open", CreatedAt: nowStr(), UpdatedAt: nowStr(),
	}
	if err := s.createConversation(r.Context(), c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) getConversationHandler(w http.ResponseWriter, r *http.Request) {
	c, ok := s.getConversation(r.Context(), chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "conversation not found")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *server) listMessagesHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": s.listMessages(r.Context(), chi.URLParam(r, "id")),
	})
}

// postPrompt pushes a user prompt into a conversation. This is the human/app
// entry point ("push a prompt to the table"). Server mode answers it via the
// runner; agent mode surfaces it in the agent's MCP inbox.
func (s *server) postPrompt(w http.ResponseWriter, r *http.Request) {
	conv, ok := s.getConversation(r.Context(), chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "conversation not found")
		return
	}
	var in struct {
		Body string `json:"body"`
		Meta string `json:"meta"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body is required")
		return
	}
	m, err := s.receivePrompt(r.Context(), conv, in.Body, in.Meta)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// ---- agent surface (token-guarded) ----

// inbox returns the calling agent's pending prompts. The MCP bridge polls this.
func (s *server) inbox(w http.ResponseWriter, r *http.Request) {
	agent, _ := agentFromCtx(r.Context())
	msgs := s.pendingForAgent(r.Context(), agent.ID, 20)
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent.Name, "messages": msgs})
}

// reply posts an agent reply into a conversation the agent owns.
func (s *server) reply(w http.ResponseWriter, r *http.Request) {
	agent, _ := agentFromCtx(r.Context())
	var in struct {
		ConversationID string `json:"conversation_id"`
		Body           string `json:"body"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Body) == "" || in.ConversationID == "" {
		writeErr(w, http.StatusBadRequest, "conversation_id and body are required")
		return
	}
	conv, ok := s.getConversation(r.Context(), in.ConversationID)
	if !ok || conv.AgentID != agent.ID {
		writeErr(w, http.StatusForbidden, "conversation not found for this agent")
		return
	}
	// Resolve the oldest still-open prompt in this conversation.
	promptID := ""
	for _, m := range s.pendingForAgent(r.Context(), agent.ID, 50) {
		if m.ConversationID == conv.ID {
			promptID = m.ID
			break
		}
	}
	m, err := s.postReply(r.Context(), conv, in.Body, promptID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setAgentStatus(r.Context(), agent.ID, "idle")
	writeJSON(w, http.StatusCreated, m)
}

// typing broadcasts a typing indicator for a conversation the agent owns.
func (s *server) typing(w http.ResponseWriter, r *http.Request) {
	agent, _ := agentFromCtx(r.Context())
	var in struct {
		ConversationID string `json:"conversation_id"`
	}
	_ = decode(r, &in)
	if conv, ok := s.getConversation(r.Context(), in.ConversationID); ok && conv.AgentID == agent.ID {
		s.setAgentStatus(r.Context(), agent.ID, "busy")
		s.emit("typing", map[string]any{"conversation_id": conv.ID, "agent": agent.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// history returns the full transcript of a conversation the agent owns, so the
// agent can rebuild context each turn.
func (s *server) history(w http.ResponseWriter, r *http.Request) {
	agent, _ := agentFromCtx(r.Context())
	conv, ok := s.getConversation(r.Context(), r.URL.Query().Get("conversation_id"))
	if !ok || conv.AgentID != agent.ID {
		writeErr(w, http.StatusForbidden, "conversation not found for this agent")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": conv,
		"messages":     s.listMessages(r.Context(), conv.ID),
	})
}
