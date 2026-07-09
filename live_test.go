package live

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestServer wires a server against an in-memory SQLite DB (the testDB hook),
// bypassing the kernel so the store + loop are unit-testable.
func newTestServer(t *testing.T) *server {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s := &server{testDB: db, hub: newHub(), mode: ModeServer, channels: map[string]Channel{}, responder: &echoResponder{}}
	if err := s.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestServerDrivenLoop proves the whole server-driven path: seed an agent +
// conversation, push a prompt, run one runner tick, and assert an agent reply is
// stored with the echo responder's format.
func TestServerDrivenLoop(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)

	agent := Agent{ID: genID(), Name: "Ada", Provider: "echo", Status: "idle", Token: genToken(), CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.createAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	conv := Conversation{ID: genID(), AgentID: agent.ID, Channel: "chat", Status: "open", CreatedAt: nowStr(), UpdatedAt: nowStr()}
	if err := s.createConversation(ctx, conv); err != nil {
		t.Fatal(err)
	}

	if _, err := s.receivePrompt(ctx, conv, "hello there", ""); err != nil {
		t.Fatal(err)
	}

	r := newRunner(s)
	if did := r.tick(ctx); !did {
		t.Fatal("runner did not claim the pending prompt")
	}

	msgs := s.listMessages(ctx, conv.ID)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (prompt+reply), got %d", len(msgs))
	}
	reply := msgs[1]
	if reply.Role != RoleAgent {
		t.Fatalf("want agent reply, got role %q", reply.Role)
	}
	if !strings.Contains(reply.Body, "You said:") || !strings.Contains(reply.Body, "hello there") {
		t.Fatalf("unexpected reply body: %q", reply.Body)
	}
	if msgs[0].Status != MsgDone {
		t.Fatalf("prompt should be marked done, got %q", msgs[0].Status)
	}
}

// TestAgentInboxAndReply proves the agent-driven path: a pending prompt shows in
// the agent's inbox, and postReply (what /reply calls) resolves it.
func TestAgentInboxAndReply(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)

	agent := Agent{ID: genID(), Name: "Ada", Provider: "claude", Status: "idle", Token: genToken(), CreatedAt: nowStr(), UpdatedAt: nowStr()}
	_ = s.createAgent(ctx, agent)
	conv := Conversation{ID: genID(), AgentID: agent.ID, Channel: "chat", Status: "open", CreatedAt: nowStr(), UpdatedAt: nowStr()}
	_ = s.createConversation(ctx, conv)
	prompt, _ := s.receivePrompt(ctx, conv, "what is 2+2?", "")

	inbox := s.pendingForAgent(ctx, agent.ID, 20)
	if len(inbox) != 1 || inbox[0].ID != prompt.ID {
		t.Fatalf("prompt not in agent inbox: %+v", inbox)
	}

	if _, err := s.postReply(ctx, conv, "4", prompt.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.pendingForAgent(ctx, agent.ID, 20); len(got) != 0 {
		t.Fatalf("inbox should be empty after reply, got %d", len(got))
	}

	// token auth lookup used by the /inbox and /reply middleware
	if a, ok := s.getAgentByToken(ctx, agent.Token); !ok || a.ID != agent.ID {
		t.Fatal("getAgentByToken failed")
	}
}

// TestIngestRoutesByRef proves inbound third-party routing: the same external_ref
// reuses one conversation; a reply delivers through the registered channel.
func TestIngestRoutesByRef(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	rec := &recordingChannel{}
	s.channels["whatsapp"] = rec

	agent := Agent{ID: genID(), Name: "Ada", Provider: "echo", Status: "idle", Token: genToken(), CreatedAt: nowStr(), UpdatedAt: nowStr()}
	_ = s.createAgent(ctx, agent)
	svc := &Service{s}

	m1, err := svc.Ingest(ctx, "whatsapp", "+15555550123", "hi")
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := svc.Ingest(ctx, "whatsapp", "+15555550123", "still me")
	if m1.ConversationID != m2.ConversationID {
		t.Fatal("same external_ref should reuse one conversation")
	}

	conv, _ := s.getConversation(ctx, m1.ConversationID)
	if _, err := s.postReply(ctx, conv, "hello back", m1.ID); err != nil {
		t.Fatal(err)
	}
	if rec.count != 1 || rec.last.Body != "hello back" {
		t.Fatalf("channel did not receive the reply: count=%d last=%q", rec.count, rec.last.Body)
	}
}

type recordingChannel struct {
	count int
	last  Message
}

func (c *recordingChannel) Name() string { return "whatsapp" }
func (c *recordingChannel) Deliver(_ context.Context, _ Conversation, m Message) error {
	c.count++
	c.last = m
	return nil
}
