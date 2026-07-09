package live

import (
	"context"
	"os"

	"github.com/togo-framework/togo"
)

// Service is the public handle channel drivers and app code use to drive the live
// plugin. Retrieve it with FromKernel(k). The plugin publishes itself into the
// kernel container during Boot.
type Service struct{ s *server }

// FromKernel returns the live Service if the plugin is installed.
func FromKernel(k *togo.Kernel) (*Service, bool) {
	if k == nil {
		return nil, false
	}
	if v, ok := k.Get("live"); ok {
		if svc, ok := v.(*Service); ok {
			return svc, true
		}
	}
	return nil, false
}

// Ingest turns an inbound third-party message into a user prompt on the right
// conversation. It routes by (channel, externalRef): an existing conversation is
// reused, otherwise a new one is opened against the channel's default agent
// (LIVE_CHANNEL_AGENT, else the first registered agent). This is the entry point
// an inbound channel webhook (WhatsApp/Slack/…) calls. The reply is later
// delivered back out through the matching Channel.Deliver.
func (svc *Service) Ingest(ctx context.Context, channel, externalRef, body string) (Message, error) {
	conv, ok := svc.s.getConversationByRef(ctx, channel, externalRef)
	if !ok {
		agentID := os.Getenv("LIVE_CHANNEL_AGENT")
		if agentID == "" {
			if as := svc.s.listAgents(ctx); len(as) > 0 {
				agentID = as[0].ID
			}
		}
		if agentID == "" {
			return Message{}, errNoDB // no agent to route to
		}
		conv = Conversation{
			ID: genID(), AgentID: agentID, Channel: channel, ExternalRef: externalRef,
			Title: channel + ":" + externalRef, Status: "open", CreatedAt: nowStr(), UpdatedAt: nowStr(),
		}
		if err := svc.s.createConversation(ctx, conv); err != nil {
			return Message{}, err
		}
	}
	return svc.s.receivePrompt(ctx, conv, body, "")
}

// Kernel exposes the underlying togo kernel to channel drivers (for config/logs).
func (svc *Service) Kernel() *togo.Kernel { return svc.s.k }
