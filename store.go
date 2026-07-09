package live

import (
	"context"
	"database/sql"
	"strings"
)

// ---- domain model ----

// Agent is a live AI agent: a persona bound to an execution backend (claude or
// omni, typically running inside its own Coder workspace) and addressed by a
// scoped token. The token authenticates the agent's MCP bridge to /inbox and
// /reply so it can read prompts and push responses.
type Agent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Persona   string `json:"persona"`   // system prompt / character
	Provider  string `json:"provider"`  // claude | omni | echo
	Workspace string `json:"workspace"` // Coder workspace name (agent-driven mode)
	Status    string `json:"status"`    // idle | busy | offline
	Token     string `json:"token"`     // bearer token for the MCP bridge (write-only)
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Conversation is one thread with an agent on one channel. external_ref binds it
// to a third-party thread (a WhatsApp number, a Slack thread ts, …) so inbound
// provider callbacks can be routed back to the right conversation.
type Conversation struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Channel     string `json:"channel"`      // table | chat | whatsapp | slack | discord
	ExternalRef string `json:"external_ref"` // provider thread id (whatsapp msisdn, …)
	Title       string `json:"title"`
	Status      string `json:"status"` // open | closed
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Message is one turn. role=user with status=pending is an inbound prompt waiting
// to be answered; role=agent is the response. Inserting a user Message is "push a
// prompt to the table".
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`   // user | agent | system
	Body           string `json:"body"`
	Status         string `json:"status"` // pending | processing | done | error
	Meta           string `json:"meta"`   // opaque JSON
	ClaimedBy      string `json:"claimed_by"`
	ClaimedAt      string `json:"claimed_at"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// Message roles / statuses.
const (
	RoleUser   = "user"
	RoleAgent  = "agent"
	RoleSystem = "system"

	MsgPending    = "pending"
	MsgProcessing = "processing"
	MsgDone       = "done"
	MsgError      = "error"
)

// ---- schema ----

func (s *server) migrate(ctx context.Context) error {
	db, _ := s.db(ctx)
	if db == nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS live_agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			persona TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT 'echo',
			workspace TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'idle',
			token TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS live_conversations (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT 'chat',
			external_ref TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'open',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS live_messages (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			body TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			meta TEXT NOT NULL DEFAULT '',
			claimed_by TEXT NOT NULL DEFAULT '',
			claimed_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// ---- agents ----

func (s *server) createAgent(ctx context.Context, a Agent) error {
	db, ph := s.db(ctx)
	if db == nil {
		return errNoDB
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO live_agents (id,name,persona,provider,workspace,status,token,created_at,updated_at) VALUES ("+phs(ph, 9)+")",
		a.ID, a.Name, a.Persona, a.Provider, a.Workspace, a.Status, a.Token, a.CreatedAt, a.UpdatedAt)
	return err
}

const agentCols = "id,name,persona,provider,workspace,status,token,created_at,updated_at"

func scanAgent(sc scanner) (Agent, error) {
	var a Agent
	err := sc.Scan(&a.ID, &a.Name, &a.Persona, &a.Provider, &a.Workspace, &a.Status, &a.Token, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *server) getAgent(ctx context.Context, id string) (Agent, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Agent{}, false
	}
	a, err := scanAgent(db.QueryRowContext(ctx, "SELECT "+agentCols+" FROM live_agents WHERE id="+ph(1), id))
	return a, err == nil
}

func (s *server) getAgentByToken(ctx context.Context, token string) (Agent, bool) {
	db, ph := s.db(ctx)
	if db == nil || token == "" {
		return Agent{}, false
	}
	a, err := scanAgent(db.QueryRowContext(ctx, "SELECT "+agentCols+" FROM live_agents WHERE token="+ph(1), token))
	return a, err == nil
}

func (s *server) listAgents(ctx context.Context) []Agent {
	db, _ := s.db(ctx)
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, "SELECT "+agentCols+" FROM live_agents ORDER BY created_at ASC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		if a, err := scanAgent(rows); err == nil {
			a.Token = "" // never leak tokens in list responses
			out = append(out, a)
		}
	}
	return out
}

func (s *server) setAgentStatus(ctx context.Context, id, status string) {
	db, ph := s.db(ctx)
	if db == nil {
		return
	}
	_, _ = db.ExecContext(ctx, "UPDATE live_agents SET status="+ph(1)+", updated_at="+ph(2)+" WHERE id="+ph(3), status, nowStr(), id)
}

// ---- conversations ----

func (s *server) createConversation(ctx context.Context, c Conversation) error {
	db, ph := s.db(ctx)
	if db == nil {
		return errNoDB
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO live_conversations (id,agent_id,channel,external_ref,title,status,created_at,updated_at) VALUES ("+phs(ph, 8)+")",
		c.ID, c.AgentID, c.Channel, c.ExternalRef, c.Title, c.Status, c.CreatedAt, c.UpdatedAt)
	return err
}

const convCols = "id,agent_id,channel,external_ref,title,status,created_at,updated_at"

func scanConv(sc scanner) (Conversation, error) {
	var c Conversation
	err := sc.Scan(&c.ID, &c.AgentID, &c.Channel, &c.ExternalRef, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *server) getConversation(ctx context.Context, id string) (Conversation, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Conversation{}, false
	}
	c, err := scanConv(db.QueryRowContext(ctx, "SELECT "+convCols+" FROM live_conversations WHERE id="+ph(1), id))
	return c, err == nil
}

// getConversationByRef finds a conversation for a third-party thread (used by
// inbound channel webhooks to route a message back to its conversation).
func (s *server) getConversationByRef(ctx context.Context, channel, ref string) (Conversation, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Conversation{}, false
	}
	c, err := scanConv(db.QueryRowContext(ctx,
		"SELECT "+convCols+" FROM live_conversations WHERE channel="+ph(1)+" AND external_ref="+ph(2)+" ORDER BY created_at DESC LIMIT 1", channel, ref))
	return c, err == nil
}

func (s *server) listConversations(ctx context.Context, agentID string) []Conversation {
	db, ph := s.db(ctx)
	if db == nil {
		return nil
	}
	var (
		rows *sql.Rows
		err  error
	)
	if agentID != "" {
		rows, err = db.QueryContext(ctx, "SELECT "+convCols+" FROM live_conversations WHERE agent_id="+ph(1)+" ORDER BY updated_at DESC", agentID)
	} else {
		rows, err = db.QueryContext(ctx, "SELECT "+convCols+" FROM live_conversations ORDER BY updated_at DESC")
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		if c, err := scanConv(rows); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func (s *server) touchConversation(ctx context.Context, id string) {
	db, ph := s.db(ctx)
	if db == nil {
		return
	}
	_, _ = db.ExecContext(ctx, "UPDATE live_conversations SET updated_at="+ph(1)+" WHERE id="+ph(2), nowStr(), id)
}

// ---- messages ----

func (s *server) createMessage(ctx context.Context, m Message) error {
	db, ph := s.db(ctx)
	if db == nil {
		return errNoDB
	}
	_, err := db.ExecContext(ctx,
		"INSERT INTO live_messages (id,conversation_id,role,body,status,meta,claimed_by,claimed_at,created_at,updated_at) VALUES ("+phs(ph, 10)+")",
		m.ID, m.ConversationID, m.Role, m.Body, m.Status, m.Meta, m.ClaimedBy, m.ClaimedAt, m.CreatedAt, m.UpdatedAt)
	if err == nil {
		s.touchConversation(ctx, m.ConversationID)
	}
	return err
}

const msgCols = "id,conversation_id,role,body,status,meta,claimed_by,claimed_at,created_at,updated_at"

func scanMsg(sc scanner) (Message, error) {
	var m Message
	err := sc.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Body, &m.Status, &m.Meta, &m.ClaimedBy, &m.ClaimedAt, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (s *server) getMessage(ctx context.Context, id string) (Message, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Message{}, false
	}
	m, err := scanMsg(db.QueryRowContext(ctx, "SELECT "+msgCols+" FROM live_messages WHERE id="+ph(1), id))
	return m, err == nil
}

// listMessages returns the full transcript of a conversation, oldest first.
func (s *server) listMessages(ctx context.Context, conversationID string) []Message {
	db, ph := s.db(ctx)
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, "SELECT "+msgCols+" FROM live_messages WHERE conversation_id="+ph(1)+" ORDER BY created_at ASC", conversationID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		if m, err := scanMsg(rows); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (s *server) setMessageStatus(ctx context.Context, id, status string) {
	db, ph := s.db(ctx)
	if db == nil {
		return
	}
	_, _ = db.ExecContext(ctx, "UPDATE live_messages SET status="+ph(1)+", updated_at="+ph(2)+" WHERE id="+ph(3), status, nowStr(), id)
}

// claimNextPending atomically claims the oldest pending user message (optionally
// scoped to one agent's conversations), so multiple runners share one queue
// without double-answering. Returns the claimed message and its conversation.
func (s *server) claimNextPending(ctx context.Context, claimer, agentID string) (Message, Conversation, bool) {
	db, ph := s.db(ctx)
	if db == nil {
		return Message{}, Conversation{}, false
	}
	q := "SELECT m.id FROM live_messages m JOIN live_conversations c ON c.id=m.conversation_id WHERE m.role=" + ph(1) + " AND m.status=" + ph(2)
	args := []any{RoleUser, MsgPending}
	if agentID != "" {
		q += " AND c.agent_id=" + ph(3)
		args = append(args, agentID)
	}
	q += " ORDER BY m.created_at ASC LIMIT 1"
	var id string
	if err := db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return Message{}, Conversation{}, false
	}
	res, err := db.ExecContext(ctx,
		"UPDATE live_messages SET status="+ph(1)+", claimed_by="+ph(2)+", claimed_at="+ph(3)+", updated_at="+ph(4)+" WHERE id="+ph(5)+" AND status="+ph(6),
		MsgProcessing, claimer, nowStr(), nowStr(), id, MsgPending)
	if err != nil {
		return Message{}, Conversation{}, false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Message{}, Conversation{}, false // lost the race
	}
	m, ok := s.getMessage(ctx, id)
	if !ok {
		return Message{}, Conversation{}, false
	}
	conv, _ := s.getConversation(ctx, m.ConversationID)
	return m, conv, true
}

// pendingForAgent returns the pending user messages for an agent's conversations
// (the agent-driven MCP inbox), oldest first.
func (s *server) pendingForAgent(ctx context.Context, agentID string, limit int) []Message {
	db, ph := s.db(ctx)
	if db == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx,
		"SELECT "+prefixCols("m", msgCols)+" FROM live_messages m JOIN live_conversations c ON c.id=m.conversation_id WHERE c.agent_id="+ph(1)+" AND m.role="+ph(2)+" AND m.status="+ph(3)+" ORDER BY m.created_at ASC LIMIT "+itoa(limit),
		agentID, RoleUser, MsgPending)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		if m, err := scanMsg(rows); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// ---- small SQL helpers ----

type scanner interface {
	Scan(dest ...any) error
}

// phs builds a comma-joined placeholder list "$1,$2,..." (or "?,?,...").
func phs(ph func(int) string, n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = ph(i + 1)
	}
	return strings.Join(parts, ",")
}

// prefixCols prefixes each column in a comma list with "<alias>." for JOIN scans.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + c
	}
	return strings.Join(parts, ",")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
