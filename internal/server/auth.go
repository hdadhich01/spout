package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// authConfig captures the auth posture at startup. Two credentials:
//
//   - Ingest token (Bearer): the CLI's "send output" credential. Grants the
//     data + ingest APIs. Managed via `spout server token …` / the /admin UI.
//   - Admin: the dashboard + token management. Granted to same-box (loopback)
//     requests automatically, or to a remote browser that logged in with
//     SPOUT_ADMIN_PASSWORD (a session cookie). Browsers can't send Bearer
//     headers, so viewing is admin-gated by design.
//
// SPOUT_PUBLIC=true bypasses everything (hosted / URL-as-auth). With nothing
// configured, same-box use still works (loopback = admin) and remote access is
// denied by default — the safe self-host posture.
type authConfig struct {
	tokens    *TokenStore
	adminPass string // SPOUT_ADMIN_PASSWORD; "" = no remote admin login
	public    bool   // SPOUT_PUBLIC=true → full bypass
}

func loadAuthConfig(tokens *TokenStore) authConfig {
	return authConfig{
		tokens:    tokens,
		adminPass: os.Getenv("SPOUT_ADMIN_PASSWORD"),
		public:    os.Getenv("SPOUT_PUBLIC") == "true",
	}
}

// adminCookie is the session cookie name set by /admin/login.
const adminCookie = "spout_admin"

// publicPaths bypass auth entirely. Health lets CLIs detect a spout server
// before they hold a token; the login page must be reachable to log in.
func isPublicPath(p string) bool {
	return p == "/api/health" ||
		p == "/admin/login" ||
		p == "/admin/logout" ||
		strings.HasPrefix(p, "/static/")
}

// isPage reports whether a path is a browser page (needs admin, since browsers
// can't present a Bearer token).
func isPage(p string) bool {
	return p == "/" || p == "/admin" || strings.HasPrefix(p, "/r/")
}

// isLocal reports a same-box request: a loopback connection IP with no
// proxy-forwarding header. The forwarded-header check stops a same-box reverse
// proxy from making every proxied request look local.
func (cfg authConfig) isLocal(c fiber.Ctx) bool {
	if c.Get("X-Forwarded-For") != "" || c.Get("X-Forwarded-Host") != "" {
		return false
	}
	ip := c.IP()
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}

// isAdmin reports whether the request may view the dashboard + manage tokens.
func (cfg authConfig) isAdmin(c fiber.Ctx) bool {
	if cfg.isLocal(c) {
		return true
	}
	return cfg.adminPass != "" && c.Cookies(adminCookie) == cfg.adminPass
}

func bearer(c fiber.Ctx) string {
	auth := c.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[len("Bearer "):]
	}
	return ""
}

// active reports whether any auth is configured. With nothing set up, the
// server stays open (back-compat / personal use on a trusted box); configuring
// a token or an admin password locks it down.
func (cfg authConfig) active() bool {
	return cfg.adminPass != "" || cfg.tokens.HasAny()
}

// middleware enforces the posture described on authConfig.
func (cfg authConfig) middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		if cfg.public || isPublicPath(c.Path()) || !cfg.active() {
			return c.Next()
		}
		p := c.Path()
		admin := cfg.isAdmin(c)

		// Admin-only: token management, /api/admin/* (whoami etc.), and pages.
		if strings.HasPrefix(p, "/api/tokens") || strings.HasPrefix(p, "/api/admin") || isPage(p) {
			if admin {
				return c.Next()
			}
			if isPage(p) {
				return loginRedirect(c)
			}
			return c.Status(401).SendString("admin required")
		}

		// Data + ingest: admin (same box / logged in) or a valid ingest token.
		if admin || cfg.tokens.Valid(bearer(c)) {
			return c.Next()
		}
		return c.Status(401).SendString("auth required")
	}
}

func loginRedirect(c fiber.Ctx) error {
	c.Set("Location", "/admin/login")
	return c.SendStatus(fiber.StatusFound)
}

// loginHandlers registers the minimal remote-admin login (password → cookie).
func (cfg authConfig) registerLogin(app *fiber.App) {
	app.Get("/admin/login", func(c fiber.Ctx) error {
		data, err := staticFS.ReadFile("static/login.html")
		if err != nil {
			return fmt.Errorf("reading login.html: %w", err)
		}
		c.Set("Content-Type", "text/html")
		return c.Send(data)
	})

	app.Post("/admin/login", func(c fiber.Ctx) error {
		if cfg.adminPass == "" {
			return c.Status(403).SendString("remote login disabled (set SPOUT_ADMIN_PASSWORD)")
		}
		if c.FormValue("password") != cfg.adminPass {
			c.Set("Location", "/admin/login?error=1")
			return c.SendStatus(fiber.StatusFound)
		}
		c.Cookie(&fiber.Cookie{
			Name:     adminCookie,
			Value:    cfg.adminPass,
			Path:     "/",
			HTTPOnly: true,
			SameSite: "Lax",
		})
		c.Set("Location", "/admin")
		return c.SendStatus(fiber.StatusFound)
	})

	app.Get("/admin/logout", func(c fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{Name: adminCookie, Value: "", Path: "/", MaxAge: -1})
		c.Set("Location", "/admin/login")
		return c.SendStatus(fiber.StatusFound)
	})
}
