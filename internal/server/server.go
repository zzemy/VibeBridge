package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	SessionToken string
	WebDir       string
}

type Server struct {
	config   Config
	upgrader websocket.Upgrader
}

type ClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

type ServerMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

func New(config Config) *Server {
	return &Server{
		config: config,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.validToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session token"})
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	})

	if err := conn.WriteJSON(ServerMessage{Type: "output", Data: "connected to VibeBridge echo server\r\n"}); err != nil {
		return
	}

	for {
		var msg ClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "input":
			if err := conn.WriteJSON(ServerMessage{Type: "output", Data: "echo: " + msg.Data + "\r\n"}); err != nil {
				return
			}
		case "ping":
			if err := conn.WriteJSON(ServerMessage{Type: "pong"}); err != nil {
				return
			}
		default:
			if err := conn.WriteJSON(ServerMessage{Type: "error", Data: "unsupported message type"}); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && strings.HasPrefix(r.URL.Path, "/ws") {
		http.NotFound(w, r)
		return
	}

	if s.config.WebDir == "" {
		writeFallback(w)
		return
	}

	indexPath := filepath.Join(s.config.WebDir, "index.html")
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		writeFallback(w)
		return
	}

	fileServer := http.FileServer(http.Dir(s.config.WebDir))
	fileServer.ServeHTTP(w, r)
}

func (s *Server) validToken(r *http.Request) bool {
	token := r.URL.Query().Get("token")
	return token != "" && token == s.config.SessionToken
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFallback(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>VibeBridge</title>
  <style>
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #09090b; color: #fafafa; font-family: ui-sans-serif, system-ui, sans-serif; }
    main { max-width: 32rem; padding: 2rem; }
    code { color: #a7f3d0; }
  </style>
</head>
<body>
  <main>
    <h1>VibeBridge backend is running</h1>
    <p>Build the frontend with <code>pnpm --dir web build</code>, or run Vite dev server during development.</p>
  </main>
</body>
</html>`))
}
