package server

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/hdadhich01/spout/internal/store"
)

// newTestApp returns a fiber app + the underlying store for assertions.
// Each test uses t.Setenv so configs are isolated.
func newTestApp(t *testing.T, env map[string]string) (*fiber.App, store.Store) {
	t.Helper()
	// Isolate the ingest-token file from the real ~/.config so tests never
	// read or write the developer's tokens.
	t.Setenv("SPOUT_TOKENS", filepath.Join(t.TempDir(), "tokens.json"))
	for k, v := range env {
		t.Setenv(k, v)
	}
	dir := filepath.Join(t.TempDir(), "store")
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return New(st), st
}

func mustGet(t *testing.T, app *fiber.App, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func mustPost(t *testing.T, app *fiber.App, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func TestAuth_OpenWhenNoToken(t *testing.T) {
	app, _ := newTestApp(t, nil)
	resp := mustGet(t, app, "/api/runs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 (open mode), got %d", resp.StatusCode)
	}
}

func TestAuth_HealthAlwaysPublic(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/health", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("/api/health should be public even with token, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) == "" {
		t.Fatal("empty health body")
	}
}

func TestAuth_RejectsMissingToken(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/runs", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("missing token should give 401, got %d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongToken(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/runs", map[string]string{
		"Authorization": "Bearer wrong-token",
	})
	if resp.StatusCode != 401 {
		t.Fatalf("wrong token should give 401, got %d", resp.StatusCode)
	}
}

func TestAuth_RejectsMalformedHeader(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/runs", map[string]string{
		"Authorization": "Basic abc123", // wrong scheme
	})
	if resp.StatusCode != 401 {
		t.Fatalf("malformed auth should give 401, got %d", resp.StatusCode)
	}
}

func TestAuth_AcceptsCorrectToken(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/runs", map[string]string{
		"Authorization": "Bearer secret",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("correct token should give 200, got %d", resp.StatusCode)
	}
}

func TestAuth_PublicModeBypassesToken(t *testing.T) {
	// Both SPOUT_TOKEN and SPOUT_PUBLIC set: public wins, token ignored.
	app, _ := newTestApp(t, map[string]string{
		"SPOUT_TOKEN":  "secret",
		"SPOUT_PUBLIC": "true",
	})
	resp := mustGet(t, app, "/api/runs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("public mode should bypass token, got %d", resp.StatusCode)
	}
}

func TestAuth_TokenAppliesToWriteEndpoints(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustPost(t, app, "/api/run", nil)
	// 400 (bad json) or 401 (auth) — we want auth to fire first.
	if resp.StatusCode != 401 {
		t.Fatalf("POST without token should give 401, got %d", resp.StatusCode)
	}
}

func TestAuth_IngestTokenReadsData(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/api/runs", map[string]string{"Authorization": "Bearer secret"})
	if resp.StatusCode != 200 {
		t.Fatalf("ingest token should read data, got %d", resp.StatusCode)
	}
}

func TestAuth_TokenManagementIsAdminOnly(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	// A valid ingest token must NOT be able to manage tokens (privilege).
	resp := mustGet(t, app, "/api/tokens", map[string]string{"Authorization": "Bearer secret"})
	if resp.StatusCode != 401 {
		t.Fatalf("ingest token must not reach /api/tokens, got %d", resp.StatusCode)
	}
}

func TestAuth_LockedPageRedirectsToLogin(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_TOKEN": "secret"})
	resp := mustGet(t, app, "/admin", nil)
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/admin/login" {
		t.Fatalf("locked /admin should 302→/admin/login, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestAuth_RemoteLoginThenView(t *testing.T) {
	app, _ := newTestApp(t, map[string]string{"SPOUT_ADMIN_PASSWORD": "hunter2"})

	req, _ := http.NewRequest("POST", "/admin/login", strings.NewReader("password=hunter2"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("login POST: %v", err)
	}
	if resp.StatusCode != 302 {
		t.Fatalf("login should redirect, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Set-Cookie"), adminCookie+"=hunter2") {
		t.Fatalf("expected admin cookie, got %q", resp.Header.Get("Set-Cookie"))
	}

	// The cookie grants the dashboard.
	resp2 := mustGet(t, app, "/admin", map[string]string{"Cookie": adminCookie + "=hunter2"})
	if resp2.StatusCode != 200 {
		t.Fatalf("admin cookie should grant /admin, got %d", resp2.StatusCode)
	}
}
