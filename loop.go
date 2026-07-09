package live

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/togo-framework/togo"
)

// ---- server-driven runner ----

type runner struct {
	s       *server
	poll    time.Duration
	agentID string // "" = answer prompts for any agent
	name    string
}

func newRunner(s *server) *runner {
	poll, _ := strconv.Atoi(env("LIVE_POLL_SECONDS", "3"))
	if poll < 2 {
		poll = 2
	}
	return &runner{
		s:       s,
		poll:    time.Duration(poll) * time.Second,
		agentID: os.Getenv("LIVE_AGENT_ID"),
		name:    env("LIVE_RUNNER_NAME", "live-runner"),
	}
}

func (r *runner) loop(ctx context.Context) {
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Drain everything ready this tick.
			for r.tick(ctx) {
			}
		}
	}
}

// tick claims and answers one pending prompt; returns true if it did work.
func (r *runner) tick(ctx context.Context) bool {
	msg, conv, ok := r.s.claimNextPending(ctx, r.name, r.agentID)
	if !ok {
		return false
	}
	r.answer(ctx, msg, conv)
	return true
}

func (r *runner) answer(ctx context.Context, prompt Message, conv Conversation) {
	agent, ok := r.s.getAgent(ctx, conv.AgentID)
	if !ok {
		r.s.setMessageStatus(ctx, prompt.ID, MsgError)
		return
	}
	r.s.setAgentStatus(ctx, agent.ID, "busy")
	r.s.emit("typing", map[string]any{"conversation_id": conv.ID, "agent": agent.Name})

	// Prior transcript = everything before this prompt.
	var history []Message
	for _, m := range r.s.listMessages(ctx, conv.ID) {
		if m.ID == prompt.ID {
			break
		}
		history = append(history, m)
	}

	rctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	reply, err := r.s.responder.Respond(rctx, agent, conv, history, prompt)
	r.s.setAgentStatus(ctx, agent.ID, "idle")
	if err != nil {
		r.s.setMessageStatus(ctx, prompt.ID, MsgError)
		_, _ = r.s.postReply(ctx, conv, "⚠️ "+trim(err.Error(), 500), prompt.ID)
		return
	}
	_, _ = r.s.postReply(ctx, conv, strings.TrimSpace(reply), prompt.ID)
}

// ---- responders ----

func newResponder(k *togo.Kernel) Responder {
	switch env("LIVE_RESPONDER", "echo") {
	case "claude":
		return &claudeResponder{bin: env("LIVE_CLAUDE_BIN", "claude")}
	case "cmd":
		return &cmdResponder{bin: os.Getenv("LIVE_RESPONDER_CMD")}
	default:
		return &echoResponder{}
	}
}

// echoResponder is deterministic — it restates the prompt with the agent persona.
// Used for demos and end-to-end tests that must not depend on a live LLM.
type echoResponder struct{}

func (e *echoResponder) Respond(_ context.Context, agent Agent, _ Conversation, history []Message, prompt Message) (string, error) {
	persona := agent.Name
	if agent.Persona != "" {
		persona = agent.Persona
	}
	return fmt.Sprintf("[%s] You said: %q (turn %d)", persona, strings.TrimSpace(prompt.Body), len(history)/2+1), nil
}

// claudeResponder shells Claude Code headless with the transcript rebuilt as
// context. Stateless per turn — memory comes from the DB, not the process.
type claudeResponder struct{ bin string }

func (c *claudeResponder) Respond(ctx context.Context, agent Agent, conv Conversation, history []Message, prompt Message) (string, error) {
	out, err := runStdin(ctx, c.bin, buildContext(agent, history, prompt), "-p", "--output-format", "text")
	if err != nil && strings.TrimSpace(out) == "" {
		return "", err
	}
	return out, nil
}

// cmdResponder pipes the built context to an arbitrary command's stdin and
// returns its stdout — an escape hatch for any other agent runtime (omni, a
// custom harness, a shell wrapper).
type cmdResponder struct{ bin string }

func (c *cmdResponder) Respond(ctx context.Context, agent Agent, conv Conversation, history []Message, prompt Message) (string, error) {
	if c.bin == "" {
		return "", fmt.Errorf("LIVE_RESPONDER_CMD not set")
	}
	parts := strings.Fields(c.bin)
	return runStdin(ctx, parts[0], buildContext(agent, history, prompt), parts[1:]...)
}

// buildContext renders persona + transcript + the new prompt into a single text
// block suitable for a headless one-shot agent invocation.
func buildContext(agent Agent, history []Message, prompt Message) string {
	var b strings.Builder
	if agent.Persona != "" {
		b.WriteString("You are ")
		b.WriteString(agent.Name)
		b.WriteString(". ")
		b.WriteString(agent.Persona)
		b.WriteString("\n\n")
	}
	if len(history) > 0 {
		b.WriteString("Conversation so far:\n")
		for _, m := range history {
			who := "User"
			if m.Role == RoleAgent {
				who = agent.Name
			}
			b.WriteString(who)
			b.WriteString(": ")
			b.WriteString(m.Body)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("User: ")
	b.WriteString(prompt.Body)
	b.WriteString("\n\nReply as ")
	b.WriteString(agent.Name)
	b.WriteString(". Keep it concise and do not prefix your name.")
	return b.String()
}

func runStdin(ctx context.Context, bin, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("%s: %v: %s", bin, err, trim(strings.TrimSpace(errb.String()), 300))
	}
	return strings.TrimSpace(out.String()), nil
}
