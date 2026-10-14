package httpx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// PtyHandler — terminal root (PTY) khusus superadmin.
//
// Transport:
//
//	POST   /api/pty                 buat sesi {cols, rows, cwd}
//	GET    /api/pty                 daftar sesi
//	DELETE /api/pty/:id             tutup sesi
//	POST   /api/pty/:id/input       {data: "..."}       ketikan
//	POST   /api/pty/:id/resize      {cols, rows}
//	GET    /api/pty/:id/poll        long-poll output (?offset=N) — lolos proxy yang mem-buffer
//	GET    /api/pty/:id/stream      SSE output (?offset=N untuk menyambung)
//	POST   /api/pty/:id/ticket      tiket WebSocket 60 detik
//	GET    /api/pty/:id/ws          WebSocket dua arah (?ticket=…&offset=N)
type PtyHandler struct {
	jwt *auth.Service
	mgr *service.PtyManager
}

func NewPtyHandler(j *auth.Service, m *service.PtyManager) *PtyHandler {
	return &PtyHandler{jwt: j, mgr: m}
}

type ptyCreateRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
	Cwd  string `json:"cwd"`
}

func whoIs(cl *auth.Claims) string {
	if cl == nil {
		return "?"
	}
	if cl.Label != "" {
		return cl.Label
	}
	if cl.UserID != 0 {
		return "admin#" + strconv.FormatInt(cl.UserID, 10)
	}
	return cl.Role
}

func (h *PtyHandler) Create(c echo.Context) error {
	var req ptyCreateRequest
	_ = c.Bind(&req)
	cl := ClaimsFrom(c)
	s, err := h.mgr.Create(req.Cols, req.Rows, strings.TrimSpace(req.Cwd), whoIs(cl))
	if err != nil {
		if errors.Is(err, service.ErrPtyLimit) || errors.Is(err, service.ErrPtyInvalidSize) {
			return response.Error(c, http.StatusTooManyRequests, err.Error())
		}
		return response.ServerError(c, "gagal membuka terminal: "+err.Error())
	}
	info := s.Info()
	auditRecord(c, "terminal.open", "pty", s.ID, map[string]any{"shell": info.Shell, "dir": info.StartDir})
	return response.Created(c, info, "Terminal dibuka")
}

func (h *PtyHandler) Index(c echo.Context) error {
	return response.OK(c, h.mgr.List())
}

func (h *PtyHandler) Destroy(c echo.Context) error {
	id := c.Param("id")
	if err := h.mgr.Close(id); err != nil {
		return response.NotFound(c, err.Error())
	}
	auditRecord(c, "terminal.close", "pty", id, nil)
	return response.OK(c, nil, "Terminal ditutup")
}

type ptyInputRequest struct {
	Data string `json:"data"`
}

func (h *PtyHandler) Input(c echo.Context) error {
	s, err := h.mgr.Get(c.Param("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	var req ptyInputRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if len(req.Data) > 256*1024 {
		return response.ValidationError(c, "input terlalu besar")
	}
	if err := s.Write([]byte(req.Data)); err != nil {
		return response.Error(c, http.StatusGone, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

type ptyResizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (h *PtyHandler) Resize(c echo.Context) error {
	s, err := h.mgr.Get(c.Param("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	var req ptyResizeRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if err := s.Resize(req.Cols, req.Rows); err != nil {
		return response.ValidationError(c, err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

func parseOffset(c echo.Context) int64 {
	v := c.QueryParam("offset")
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Stream mengirim output sebagai Server-Sent Events. Tiap event:
//
//	data: {"o":<offset awal>,"d":"<base64>","t":<truncated>}
//
// Event "exit" dikirim saat shell berakhir. Komentar ": hb" tiap 15 detik
// menjaga koneksi tetap hidup di balik Cloudflare / Nginx.
func (h *PtyHandler) Stream(c echo.Context) error {
	s, err := h.mgr.Get(c.Param("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Padding awal: beberapa proxy menahan respons kecil sebelum mengalirkannya.
	fmt.Fprintf(w, ": %s\n\n", strings.Repeat(" ", 2048))
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", mustJSON(s.Info()))
	w.Flush()

	exited, _ := s.Stream(c.Request().Context(), parseOffset(c), 15*time.Second,
		func(ch service.PtyChunk) error {
			payload := map[string]any{"o": ch.Offset, "d": base64.StdEncoding.EncodeToString(ch.Data)}
			if ch.Truncated {
				payload["t"] = 1
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", mustJSON(payload)); err != nil {
				return err
			}
			w.Flush()
			return nil
		},
		func() error {
			if _, err := fmt.Fprint(w, ": hb\n\n"); err != nil {
				return err
			}
			w.Flush()
			return nil
		},
	)
	if exited {
		fmt.Fprintf(w, "event: exit\ndata: %s\n\n", mustJSON(map[string]int{"code": s.ExitCode()}))
		w.Flush()
	}
	return nil
}

// Poll adalah long-polling: menunggu sampai ada output baru (maks ~25 detik),
// lalu langsung membalas dengan semua output yang tersedia. Setiap respons
// selesai, sehingga tetap real-time walau proxy di depan (Cloudflare / proxy
// kampus) mem-buffer respons streaming seperti SSE.
//
//	GET /api/pty/:id/poll?offset=N  →  {"o":offset,"d":"<base64>","t":0|1,"exited":bool,"code":N}
func (h *PtyHandler) Poll(c echo.Context) error {
	s, err := h.mgr.Get(c.Param("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	detach := s.Attach()
	defer detach()

	ctx, cancel := context.WithTimeout(c.Request().Context(), 25*time.Second)
	defer cancel()
	off := parseOffset(c)
	chunk, changed, finished := s.ReadFrom(off, 256*1024)
	if len(chunk.Data) == 0 && !chunk.Truncated && !finished {
		select {
		case <-changed:
			// Beri jeda singkat agar output beruntun (mis. echo + prompt)
			// terkirim dalam satu respons.
			select {
			case <-time.After(12 * time.Millisecond):
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
		chunk, _, finished = s.ReadFrom(off, 256*1024)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	payload := map[string]any{
		"o":      chunk.Offset,
		"d":      base64.StdEncoding.EncodeToString(chunk.Data),
		"exited": finished,
	}
	if chunk.Truncated {
		payload["t"] = 1
	}
	if finished {
		payload["code"] = s.ExitCode()
	}
	return c.JSON(http.StatusOK, payload)
}

var ptyUpgrader = websocket.Upgrader{
	ReadBufferSize:  8 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// WS adalah jalur cepat bila proxy meneruskan WebSocket.
// Server → klien: frame biner = output mentah; frame teks = kontrol JSON
// ({"t":"hello",...} / {"t":"exit","code":N}).
// Klien → server: {"t":"i","d":"..."} input, {"t":"r","c":cols,"r":rows} resize.
// Ticket menerbitkan tiket WebSocket berumur 60 detik untuk satu sesi, supaya
// JWT sesi (berlaku lama) tidak perlu ditaruh di URL (rawan tercatat di log
// proxy/CDN).
func (h *PtyHandler) Ticket(c echo.Context) error {
	id := c.Param("id")
	if _, err := h.mgr.Get(id); err != nil {
		return response.NotFound(c, err.Error())
	}
	t, err := h.jwt.IssueTicket("pty-ws", id, false, 60*time.Second)
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, map[string]string{"ticket": t})
}

func (h *PtyHandler) WS(c echo.Context) error {
	tc, err := h.jwt.VerifyTicket(c.QueryParam("ticket"), "pty-ws")
	if err != nil || tc.Subject != c.Param("id") {
		return response.Unauthorized(c, "tiket terminal tidak valid atau kedaluwarsa")
	}
	s, err := h.mgr.Get(c.Param("id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}

	ws, err := ptyUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return nil
	}
	defer ws.Close()

	ctx := c.Request().Context()
	writeMu := make(chan struct{}, 1)
	writeMu <- struct{}{}
	send := func(mt int, b []byte) error {
		<-writeMu
		defer func() { writeMu <- struct{}{} }()
		_ = ws.SetWriteDeadline(time.Now().Add(20 * time.Second))
		return ws.WriteMessage(mt, b)
	}

	hello, _ := json.Marshal(map[string]any{"t": "hello", "info": s.Info()})
	if err := send(websocket.TextMessage, hello); err != nil {
		return nil
	}

	// Pembaca: input + resize dari klien.
	readErr := make(chan error, 1)
	go func() {
		ws.SetReadLimit(512 * 1024)
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			var m struct {
				T string `json:"t"`
				D string `json:"d"`
				C uint16 `json:"c"`
				R uint16 `json:"r"`
			}
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			switch m.T {
			case "i":
				_ = s.Write([]byte(m.D))
			case "r":
				_ = s.Resize(m.C, m.R)
			}
		}
	}()

	streamCtx, cancel := cancelOnErr(ctx, readErr)
	defer cancel()

	exited, _ := s.Stream(streamCtx, parseOffset(c), 25*time.Second,
		func(ch service.PtyChunk) error {
			if ch.Truncated {
				b, _ := json.Marshal(map[string]any{"t": "truncated", "o": ch.Offset})
				_ = send(websocket.TextMessage, b)
			}
			if len(ch.Data) == 0 {
				return nil
			}
			return send(websocket.BinaryMessage, ch.Data)
		},
		func() error {
			<-writeMu
			defer func() { writeMu <- struct{}{} }()
			return ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		},
	)
	if exited {
		b, _ := json.Marshal(map[string]any{"t": "exit", "code": s.ExitCode()})
		_ = send(websocket.TextMessage, b)
	}
	return nil
}
