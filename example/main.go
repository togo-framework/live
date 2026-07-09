// Command live-example is a minimal, runnable togo app that boots the `live`
// plugin end-to-end with zero external dependencies.
//
// It runs entirely on SQLite (no Postgres/Redis/NATS needed) and starts the
// plugin in server-driven mode with the deterministic `echo` responder, so a
// prompt pushed to the seeded conversation is answered in-process. On boot the
// plugin seeds one agent ("Ada") and an open "Demo chat" conversation.
//
// Try it:
//
//	go run .
//	# then open http://localhost:8099/live  (chat board)
//	# or drive it over HTTP:
//	curl localhost:8099/api/live/agents
//
// Everything is configured via environment variables (the togo convention); the
// defaults below are only applied when the caller hasn't set them, so the same
// binary works against Postgres or in agent-driven mode by exporting the vars.
package main

import (
	"context"
	"log"
	"os"

	"github.com/togo-framework/togo"

	// Register the SQLite driver ("sqlite") the kernel opens by default.
	_ "modernc.org/sqlite"

	// Blank-import the live plugin: its init() self-registers with the kernel,
	// mounts /api/live/* and serves the chat board at /live.
	_ "github.com/togo-framework/live"
)

// setDefault sets an env var only if the operator hasn't already provided one,
// so the demo is turnkey but every knob stays overridable from the environment.
func setDefault(key, val string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, val)
	}
}

func main() {
	// --- togo core config (see togo/config.go) ---
	setDefault("DB_DRIVER", "sqlite")
	setDefault("DATABASE_URL", "file:./live-example.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_time_format=sqlite")
	setDefault("ADDR", ":8099")

	// --- live plugin config (see live/live.go) ---
	setDefault("LIVE_MODE", "server")     // in-app runner claims prompts + replies
	setDefault("LIVE_RESPONDER", "echo")  // deterministic; no live LLM required
	setDefault("LIVE_SEED_AGENT", "Ada")  // seed one agent + an open "Demo chat"

	// Defaults must be in place BEFORE New() reads the environment.
	k := togo.New()
	defer k.Close()

	log.Printf("live-example listening on %s  (chat board: http://localhost%s/live · API: %s/api/live)",
		k.Config.Addr, k.Config.Addr, k.Config.Addr)

	if err := k.Serve(context.Background()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
