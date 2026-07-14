package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// TerminalWS protocol (JSON lines):
//
//	client -> server:
//	  {"type":"exec","command":"ls -la"}
//	  {"type":"resize","cols":80,"rows":24}   (informational, currently ignored)
//	  {"type":"ping"}
//
//	server -> client:
//	  {"type":"ready","cwd":"...","root":"..."}
//	  {"type":"stdout","data":"..."}
//	  {"type":"stderr","data":"..."}
//	  {"type":"exit","code":0}
//	  {"type":"error","message":"..."}
//	  {"type":"pong"}

type TerminalHandler struct {
	jwt   *auth.Service
	terms *service.TerminalService
}

func NewTerminalHandler(j *auth.Service, t *service.TerminalService) *TerminalHandler {
	return &TerminalHandler{jwt: j, terms: t}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// Handle is the WebSocket endpoint: GET /api/terminal?appName=foo&token=<jwt>
func (h *TerminalHandler) Handle(c echo.Context) error {
	// Auth via ?token= since browsers can't set headers for WS easily.
	tok := c.QueryParam("token")
	if tok == "" {
		// Fall back to Authorization header (for non-browser clients).
		authz := c.Request().Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			tok = strings.TrimSpace(authz[7:])
		}
	}
	if tok == "" {
		return response.Unauthorized(c, "token tidak ditemukan")
	}
	claims, err := h.jwt.Verify(tok)
	if err != nil {
		return response.Unauthorized(c, err.Error())
	}

	appName := c.QueryParam("appName")
	if appName == "" {
		return response.ValidationError(c, map[string]string{"appName": "appName diperlukan"})
	}

	sess, err := h.terms.Open(c.Request().Context(), appName, claims)
	if err != nil {
		if errors.Is(err, service.ErrTerminalNotAllowed) {
			return response.Forbidden(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	// Send "ready"
	if err := ws.WriteJSON(map[string]any{
		"type": "ready",
		"cwd":  sess.Root,
		"root": sess.Root,
		"app":  sess.App,
		"kind": sess.Kind,
	}); err != nil {
		return nil
	}

	// Ping/pong setup
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Keepalive ticker
	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()

	// Reader goroutine to enforce read deadline / pong, but main loop drives exec.
	// We run a single command at a time per connection (no built-in shell).
	// Frontend can run multiple "exec" frames sequentially.

	// We need to be able to close the writer from exec side; use a channel.
	execDone := make(chan struct{})

	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-execDone:
				return
			case <-ticker.C:
				_ = ws.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			}
		}
	}()

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			close(execDone)
			return nil
		}

		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			_ = ws.WriteJSON(map[string]string{"type": "error", "message": "json tidak valid"})
			continue
		}

		t, _ := msg["type"].(string)
		switch t {
		case "exec":
			cmd, _ := msg["command"].(string)
			h.runCommand(c.Request().Context(), ws, sess, cmd)
		case "ping":
			_ = ws.WriteJSON(map[string]string{"type": "pong"})
		case "close":
			close(execDone)
			return nil
		default:
			_ = ws.WriteJSON(map[string]string{"type": "error", "message": "type tidak dikenal: " + t})
		}
	}
}

func (h *TerminalHandler) runCommand(parent context.Context, ws *websocket.Conn, sess *service.Session, cmd string) {
	if cmd == "" {
		_ = ws.WriteJSON(map[string]string{"type": "error", "message": "command kosong"})
		return
	}

	// Each command gets its own context so we can cancel it on new exec.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	writer := &wsWriter{ws: ws}

	type execOut struct {
		code int
		err  error
	}
	resCh := make(chan execOut, 1)
	go func() {
		code, err := h.terms.Exec(ctx, sess, cmd, writer)
		resCh <- execOut{code: code, err: err}
	}()

	out := <-resCh
	if out.err != nil {
		_ = ws.WriteJSON(map[string]string{"type": "error", "message": out.err.Error()})
		_ = ws.WriteJSON(map[string]any{"type": "exit", "code": -1})
		return
	}
	_ = ws.WriteJSON(map[string]any{"type": "exit", "code": out.code})
}

// wsWriter wraps the WebSocket conn and frames each Write as a stdout JSON message.
type wsWriter struct {
	ws *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	if err := w.ws.WriteJSON(map[string]string{
		"type": "stdout",
		"data": string(p),
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// silence unused import in some build configs
var _ = log.Println