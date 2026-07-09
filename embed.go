package live

import (
	_ "embed"
	"net/http"
)

//go:embed web/board.html
var boardHTML []byte

// serveBoard serves the self-contained live chat board. It talks to /api/live/*
// with fetch + a WebSocket to /api/live/ws, so no build step is needed.
func (s *server) serveBoard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(boardHTML)
}
