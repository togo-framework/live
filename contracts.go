package live

import (
	"context"
	"sync"

	"github.com/togo-framework/togo"
)

// Responder produces an agent reply for a prompt in server-driven mode. The loop
// hands it the agent, the conversation, the full prior transcript and the new
// user message; it returns the reply text. Implementations: echoResponder
// (deterministic, for tests/demos), claudeResponder (shells `claude -p`), and
// slotResponder (reuses the togo `impl`/`exec` provider slots, i.e. omnigent
// inside a Coder workspace). In agent-driven mode no Responder runs — the agent's
// own MCP bridge calls /reply instead.
type Responder interface {
	Respond(ctx context.Context, agent Agent, conv Conversation, history []Message, prompt Message) (string, error)
}

// Channel delivers an agent's reply out to where the conversation lives — the
// egress side. The built-in "table"/"chat" channels are in-app only (the reply
// is stored and streamed over the WS hub); third-party channels (whatsapp, slack,
// discord) are registered by driver plugins and push the reply to the provider.
// Delivery for the in-app channels is handled directly by the server, so a
// registered Channel only needs to cover its external transport.
type Channel interface {
	// Name is the conversation.channel value this handles (e.g. "whatsapp").
	Name() string
	// Deliver pushes msg (an agent reply) to conv's external thread.
	Deliver(ctx context.Context, conv Conversation, msg Message) error
}

// ChannelFactory builds a Channel from the kernel (config/secrets from env).
type ChannelFactory func(k *togo.Kernel) Channel

var (
	channelMu       sync.RWMutex
	channelRegistry = map[string]ChannelFactory{}
)

// RegisterChannel registers an egress channel driver. Call from a driver
// package's init(); the app activates it by blank-importing the package. This
// mirrors togo-framework/notifications' RegisterChannel so a live channel and a
// notifications channel are authored the same way.
func RegisterChannel(name string, f ChannelFactory) {
	channelMu.Lock()
	channelRegistry[name] = f
	channelMu.Unlock()
}

// buildChannels instantiates every registered channel for this kernel.
func buildChannels(k *togo.Kernel) map[string]Channel {
	channelMu.RLock()
	defer channelMu.RUnlock()
	out := make(map[string]Channel, len(channelRegistry))
	for name, f := range channelRegistry {
		if ch := f(k); ch != nil {
			out[name] = ch
		}
	}
	return out
}
