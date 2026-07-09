// Command live-mcp is the MCP bridge an agent runs inside its Coder workspace to
// "come alive": it exposes tools the running claude/omni calls to read prompts
// and push replies through a togo app's live plugin. The agent's loop is simply
// "call live_inbox → think → call live_reply".
//
// Config (env):
//
//	LIVE_API_URL     base URL of the togo app (default http://localhost:8080)
//	LIVE_AGENT_TOKEN bearer token from POST /api/live/agents (required)
//	MCP_HTTP_ADDR    if set, serve Streamable HTTP here instead of stdio
//
// Install into the agent's .mcp.json:
//
//	{ "mcpServers": { "live": { "command": "live-mcp",
//	    "env": { "LIVE_API_URL": "...", "LIVE_AGENT_TOKEN": "lat_..." } } } }
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/togo-framework/mcp/togomcp"
)

var (
	baseURL = strings.TrimRight(env("LIVE_API_URL", "http://localhost:8080"), "/")
	token   = os.Getenv("LIVE_AGENT_TOKEN")
	client  = &http.Client{Timeout: 30 * time.Second}
)

func main() {
	if token == "" {
		fmt.Fprintln(os.Stderr, "live-mcp: LIVE_AGENT_TOKEN is required")
		os.Exit(1)
	}
	s := togomcp.New("togo-live", "0.1.0")

	togomcp.AddTool(s, "live_inbox",
		"List the pending prompts waiting for this agent. Poll this, answer each with live_reply.",
		func(ctx context.Context, _ togomcp.NoArgs) (string, error) {
			return apiGET(ctx, "/api/live/inbox")
		})

	togomcp.AddTool(s, "live_history",
		"Get the full transcript of a conversation so you can answer with context.",
		func(ctx context.Context, in struct {
			ConversationID string `json:"conversation_id"`
		}) (string, error) {
			return apiGET(ctx, "/api/live/history?conversation_id="+in.ConversationID)
		})

	togomcp.AddTool(s, "live_typing",
		"Signal that you are working on a reply (shows a typing indicator to the user).",
		func(ctx context.Context, in struct {
			ConversationID string `json:"conversation_id"`
		}) (string, error) {
			return apiPOST(ctx, "/api/live/typing", map[string]string{"conversation_id": in.ConversationID})
		})

	togomcp.AddTool(s, "live_reply",
		"Post your reply into a conversation. This delivers it to the user over whatever channel the conversation uses (chat, WhatsApp, Slack, …).",
		func(ctx context.Context, in struct {
			ConversationID string `json:"conversation_id"`
			Body           string `json:"body"`
		}) (string, error) {
			if in.ConversationID == "" || in.Body == "" {
				return "", fmt.Errorf("conversation_id and body are required")
			}
			return apiPOST(ctx, "/api/live/reply", map[string]string{"conversation_id": in.ConversationID, "body": in.Body})
		})

	var err error
	if addr := os.Getenv("MCP_HTTP_ADDR"); addr != "" {
		err = s.RunHTTP(addr)
	} else {
		err = s.Run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "live-mcp:", err)
		os.Exit(1)
	}
}

func apiGET(ctx context.Context, path string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	return do(req)
}

func apiPOST(ctx context.Context, path string, body any) (string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func do(req *http.Request) (string, error) {
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("live api %s: %s", resp.Status, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
