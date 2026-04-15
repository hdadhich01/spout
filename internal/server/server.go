package server

import (
	"embed"
	"fmt"
	"net/http"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/hdadhich01/spout/internal/store"
	"github.com/valyala/fasthttp"
)

//go:embed static
var staticFS embed.FS

var ingestUpgrader = websocket.FastHTTPUpgrader{
	CheckOrigin:    func(ctx *fasthttp.RequestCtx) bool { return true },
	ReadBufferSize: 64 * 1024,
}

var viewerUpgrader = websocket.FastHTTPUpgrader{
	CheckOrigin:     func(ctx *fasthttp.RequestCtx) bool { return true },
	WriteBufferSize: 64 * 1024,
}

type runInfo struct {
	Name      string `json:"name"`
	Mode      string `json:"mode,omitempty"`
	Status    string `json:"status"`
	Command   string `json:"command,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	StartedAt string `json:"started_at"`
	StartedMs int64  `json:"started_ms"`
	EndedAt    string `json:"ended_at,omitempty"`
	Bytes      int64  `json:"bytes"`
	Lines      int64  `json:"lines"`
	DurationMs int64  `json:"duration_ms"`
	Active     bool   `json:"active"`
	HasExit    bool   `json:"has_exit"`
	ExitCode   int    `json:"exit_code"`
}

func sessionToInfo(s *store.Session) runInfo {
	info := runInfo{
		Name:      s.Name,
		Mode:      s.Mode,
		Status:    s.Status(),
		Command:   s.Command,
		Dir:       s.Dir,
		Host:      s.Host,
		User:      s.User,
		GitBranch: s.GitBranch,
		StartedAt: s.StartedAt.Format("2006-01-02 15:04:05"),
		StartedMs: s.StartedAt.UnixMilli(),
		Bytes:    s.BytesRecv,
		Lines:    s.Lines,
		Active:   s.Active,
		HasExit:  s.HasExit,
		ExitCode: s.ExitCode,
	}
	if !s.EndedAt.IsZero() {
		info.EndedAt = s.EndedAt.Format("2006-01-02 15:04:05")
		info.DurationMs = s.EndedAt.Sub(s.StartedAt).Milliseconds()
	} else {
		info.DurationMs = time.Since(s.StartedAt).Milliseconds()
	}
	return info
}

// New creates a Fiber app with multi-session support.
func New(st *store.Store) *fiber.App {
	app := fiber.New()

	// Pages.
	app.Get("/", func(c fiber.Ctx) error {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			return fmt.Errorf("reading index.html: %w", err)
		}
		c.Set("Content-Type", "text/html")
		return c.Send(data)
	})

	app.Get("/r/:name", func(c fiber.Ctx) error {
		data, err := staticFS.ReadFile("static/session.html")
		if err != nil {
			return fmt.Errorf("reading session.html: %w", err)
		}
		c.Set("Content-Type", "text/html")
		return c.Send(data)
	})

	// API: health check - used by clients to confirm this is a spout server.
	app.Get("/api/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"spout": true})
	})

	// API: list all runs.
	app.Get("/api/runs", func(c fiber.Ctx) error {
		sessions := st.List()
		list := make([]runInfo, len(sessions))
		for i, s := range sessions {
			list[i] = sessionToInfo(s)
		}
		return c.JSON(list)
	})

	// API: get single run.
	app.Get("/api/run/:name", func(c fiber.Ctx) error {
		sess := st.Get(c.Params("name"))
		if sess == nil {
			return c.Status(404).SendString("not found")
		}
		return c.JSON(sessionToInfo(sess))
	})

	// Raw bytes for the run - used by `spout logs <name>`.
	app.Get("/api/run/:name/raw", func(c fiber.Ctx) error {
		sess := st.Get(c.Params("name"))
		if sess == nil {
			return c.Status(404).SendString("not found")
		}
		c.Set("Content-Type", "application/octet-stream")
		return c.Send(sess.History())
	})

	// API: pre-create a run with metadata (called by spout run before tmux).
	app.Post("/api/run", func(c fiber.Ctx) error {
		var m store.RunMeta
		if err := c.Bind().JSON(&m); err != nil {
			return c.Status(http.StatusBadRequest).SendString("bad json")
		}
		if _, err := st.Create(m); err != nil {
			return c.Status(500).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// API: rename.
	app.Post("/api/run/:name/rename", func(c fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.Bind().JSON(&body); err != nil || body.Name == "" {
			return c.Status(400).SendString("missing 'name'")
		}
		if err := st.Rename(c.Params("name"), body.Name); err != nil {
			return c.Status(404).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// API: delete.
	app.Delete("/api/run/:name", func(c fiber.Ctx) error {
		if err := st.Delete(c.Params("name")); err != nil {
			return c.Status(404).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// WebSocket: viewer.
	app.Get("/ws/:name", func(c fiber.Ctx) error {
		name := string(append([]byte{}, c.Params("name")...))
		sess := st.Get(name)
		if sess == nil {
			return c.Status(404).SendString("not found")
		}

		return viewerUpgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
			defer conn.Close()

			ch, history, active := sess.Subscribe()
			if active {
				defer sess.Unsubscribe(ch)
			}

			// Replay history.
			if len(history) > 0 {
				if err := conn.WriteMessage(websocket.BinaryMessage, history); err != nil {
					return
				}
			}

			if !active {
				// Session already ended - send a status message and close.
				conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"ended"}`))
				return
			}

			// Stream live data. Channel is closed when session ends.
			for data := range ch {
				if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
					return
				}
			}

			// Session ended while we were streaming.
			conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"ended"}`))
		})
	})

	// WebSocket: CLI ingest. Session must already exist (pre-created or auto-created).
	// IMPORTANT: Copy all params/query values before the WebSocket upgrade.
	// After upgrade, fasthttp reuses the request buffer and params become garbage.
	app.Get("/ingest/:name", func(c fiber.Ctx) error {
		m := store.RunMeta{
			Name:      string(append([]byte{}, c.Params("name")...)),
			Mode:      string(append([]byte{}, c.Query("mode", "pipe")...)),
			Command:   string(append([]byte{}, c.Query("cmd", "")...)),
			Dir:       string(append([]byte{}, c.Query("dir", "")...)),
			Host:      string(append([]byte{}, c.Query("host", "")...)),
			User:      string(append([]byte{}, c.Query("user", "")...)),
			GitBranch: string(append([]byte{}, c.Query("git_branch", "")...)),
		}

		sess := st.Get(m.Name)
		if sess == nil {
			var err error
			sess, err = st.Create(m)
			if err != nil {
				return fmt.Errorf("creating session %s: %w", m.Name, err)
			}
		}

		return ingestUpgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
			defer conn.Close()
			defer func() {
				sess.Close()
				st.SaveMeta(sess)
			}()
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				sess.Write(msg)
			}
		})
	})

	return app
}
