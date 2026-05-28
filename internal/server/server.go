package server

import (
	"embed"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/hdadhich01/spout/internal/config"
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
	Job       string `json:"job,omitempty"`
	Run       string `json:"run,omitempty"`
	Label     string `json:"label,omitempty"`
	Command   string `json:"command,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Host      string `json:"host,omitempty"`
	User      string `json:"user,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`
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

// contentTypeFor maps a static asset's extension to its MIME type.
func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "application/javascript"
	case strings.HasSuffix(name, ".css"):
		return "text/css"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

func sessionToInfo(s *store.Session) runInfo {
	info := runInfo{
		Name:      s.Name,
		Mode:      s.Mode,
		Status:    s.Status(),
		Job:       s.Job,
		Run:       s.Run,
		Label:     s.Label,
		Command:   s.Command,
		Dir:       s.Dir,
		Host:      s.Host,
		User:      s.User,
		GitBranch: s.GitBranch,
		GitCommit: s.GitCommit,
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

// New creates a Fiber app with multi-session support. The store is taken
// as an interface so the binary can run against any backend (FileStore
// today; SQLiteStore + PostgresStore planned).
//
// Reads env vars at construction:
//
//	SPOUT_TOKEN           — legacy single ingest token (still accepted)
//	SPOUT_ADMIN_PASSWORD  — enables remote admin login at /admin/login
//	SPOUT_PUBLIC          — `true` bypasses all auth (hosted / URL-as-auth)
//
// Ingest tokens (the "send output" credential) live in config.TokensPath();
// manage them with `spout server token …` or the /admin UI.
func New(st store.Store) *fiber.App {
	app := fiber.New()

	// Auth posture, read once at boot.
	tokens := LoadTokenStore(config.TokensPath(), os.Getenv("SPOUT_TOKEN"))
	auth := loadAuthConfig(tokens)
	app.Use(auth.middleware())
	auth.registerLogin(app)

	// Per-session caps, read once at boot.
	caps := loadCapsConfig()
	applyCaps := func(sess *store.Session) {
		if sess != nil && caps.sessionCap > 0 {
			sess.SetCap(caps.sessionCap)
		}
	}

	// Pages. The dashboard lives at /admin (run list + token management); / is
	// a convenience redirect to it.
	serveHTML := func(file string) fiber.Handler {
		return func(c fiber.Ctx) error {
			data, err := staticFS.ReadFile("static/" + file)
			if err != nil {
				return fmt.Errorf("reading %s: %w", file, err)
			}
			c.Set("Content-Type", "text/html")
			return c.Send(data)
		}
	}
	app.Get("/", func(c fiber.Ctx) error {
		c.Set("Location", "/admin")
		return c.SendStatus(fiber.StatusFound)
	})
	app.Get("/admin", serveHTML("index.html"))

	app.Get("/r/:name", func(c fiber.Ctx) error {
		data, err := staticFS.ReadFile("static/session.html")
		if err != nil {
			return fmt.Errorf("reading session.html: %w", err)
		}
		c.Set("Content-Type", "text/html")
		return c.Send(data)
	})

	// Static assets (vendored xterm.js, CSS, favicon). Embedded in the
	// binary so the dashboard works fully offline — important for
	// self-hosted / airgapped deployments. Long cache: filenames are
	// version-pinned, content is immutable.
	app.Get("/static/*", func(c fiber.Ctx) error {
		rel := c.Params("*")
		if rel == "" || strings.Contains(rel, "..") {
			return c.SendStatus(http.StatusNotFound)
		}
		data, err := staticFS.ReadFile("static/" + rel)
		if err != nil {
			return c.SendStatus(http.StatusNotFound)
		}
		c.Set("Content-Type", contentTypeFor(rel))
		c.Set("Cache-Control", "public, max-age=31536000, immutable")
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
		// ?download=1 → browser saves the file instead of rendering inline.
		// xterm.js can't render multi-GB logs; this is the escape hatch.
		if c.Query("download") == "1" {
			c.Set("Content-Disposition", `attachment; filename="`+sess.Name+`.log"`)
		}
		return c.Send(sess.History())
	})

	// API: pre-create a run with metadata (called by spout run before tmux).
	app.Post("/api/run", func(c fiber.Ctx) error {
		var m store.RunMeta
		if err := c.Bind().JSON(&m); err != nil {
			return c.Status(http.StatusBadRequest).SendString("bad json")
		}
		sess, err := st.Create(m)
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}
		applyCaps(sess)
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

	// API: append an observability event (CLI observer → server). The body
	// is one event as JSON; the server stores it opaquely (it never parses
	// the payload), so Tier-2 encrypted envelopes pass through unchanged.
	app.Post("/api/run/:name/events", func(c fiber.Ctx) error {
		name := string(append([]byte{}, c.Params("name")...))
		body := c.Body()
		if len(body) == 0 {
			return c.Status(http.StatusBadRequest).SendString("empty body")
		}
		raw := append([]byte{}, body...) // copy off the reused fasthttp buffer
		if err := st.AppendEvent(name, raw); err != nil {
			return c.Status(500).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// API: read a run's observability events (newline-delimited JSON).
	app.Get("/api/run/:name/events", func(c fiber.Ctx) error {
		c.Set("Content-Type", "application/x-ndjson")
		return c.Send(st.Events(c.Params("name")))
	})

	// API: ingest-token management (admin-only — gated by the auth middleware).
	app.Get("/api/tokens", func(c fiber.Ctx) error {
		return c.JSON(tokens.List())
	})
	app.Post("/api/tokens", func(c fiber.Ctx) error {
		var body struct {
			Label string `json:"label"`
		}
		if err := c.Bind().JSON(&body); err != nil || strings.TrimSpace(body.Label) == "" {
			return c.Status(http.StatusBadRequest).SendString("missing 'label'")
		}
		e, err := tokens.Add(strings.TrimSpace(body.Label))
		if err != nil {
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		}
		return c.JSON(e)
	})
	app.Delete("/api/tokens/:label", func(c fiber.Ctx) error {
		// Fiber v3 doesn't auto-decode path params, so a label with spaces
		// (e.g. "yo whats good trwin") arrives URL-encoded. Decode it first.
		label := c.Params("label")
		if dec, err := url.QueryUnescape(label); err == nil {
			label = dec
		}
		if err := tokens.Revoke(label); err != nil {
			return c.Status(404).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// Bulletproof revoke endpoint — the label rides in the JSON body, so we
	// never have to worry about URL encoding. The dashboard uses this one.
	app.Post("/api/tokens/revoke", func(c fiber.Ctx) error {
		var body struct {
			Label string `json:"label"`
		}
		if err := c.Bind().JSON(&body); err != nil || strings.TrimSpace(body.Label) == "" {
			return c.Status(http.StatusBadRequest).SendString("missing 'label'")
		}
		if err := tokens.Revoke(strings.TrimSpace(body.Label)); err != nil {
			return c.Status(404).SendString(err.Error())
		}
		return c.SendString("ok")
	})

	// Who-am-I: tells the dashboard whether the viewer is on the same box
	// (local admin, no session to log out of) and whether remote login is
	// even configured (SPOUT_ADMIN_PASSWORD set).
	app.Get("/api/admin/whoami", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"local":              auth.isLocal(c),
			"remote_login_ready": auth.adminPass != "",
		})
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

			// Replay window cap. xterm.js can't render multi-MB scrollback
			// without choking; default 4 MB. If history exceeds the cap,
			// send a JSON banner so the dashboard can render a "showing
			// last N MB; download full log →" link, then send the tail.
			rcap := caps.effectiveReplayCap()
			if int64(len(history)) > rcap {
				banner := fmt.Sprintf(
					`{"truncated":true,"shown":%d,"total":%d}`,
					rcap, len(history))
				conn.WriteMessage(websocket.TextMessage, []byte(banner))
				history = history[int64(len(history))-rcap:]
			}
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

			// Stream live data + 24h hard cap on viewer connection age.
			// On long-lived viewers (rare), force a reconnect after 24h
			// to let the browser pick up a fresh state.
			deadline := time.NewTimer(24 * time.Hour)
			defer deadline.Stop()
			for {
				select {
				case data, ok := <-ch:
					if !ok {
						// Channel closed: either session ended naturally, OR
						// broadcast dropped this viewer for being slow.
						// Browser sees clean close + reconnects either way.
						conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"ended"}`))
						return
					}
					if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
						return
					}
				case <-deadline.C:
					return
				}
			}
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
			GitCommit: string(append([]byte{}, c.Query("git_commit", "")...)),
		}

		sess := st.Get(m.Name)
		if sess == nil {
			var err error
			sess, err = st.Create(m)
			if err != nil {
				return fmt.Errorf("creating session %s: %w", m.Name, err)
			}
		}
		applyCaps(sess)

		return ingestUpgrader.Upgrade(c.RequestCtx(), func(conn *websocket.Conn) {
			defer conn.Close()
			defer func() {
				sess.Close()
				st.SaveMeta(sess)
			}()
			// 24h idle read deadline. Refresh after every successful read.
			// Producer that goes silent for a full day = zombie; reap.
			conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				sess.Write(msg)
				conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
			}
		})
	})

	return app
}
