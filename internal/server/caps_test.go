package server

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hdadhich01/spout/internal/store"
)

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"100", 100},
		{"100B", 100},
		{"1K", 1024},
		{"1KB", 1024},
		{"1M", 1 << 20},
		{"100MB", 100 << 20},
		{"1G", 1 << 30},
		{"2GB", 2 << 30},
		{"1T", 1 << 40},
		{"  10mb  ", 10 << 20}, // trim + case-insensitive
		{"garbage", 0},
		{"-5MB", 0},
	}
	for _, tc := range cases {
		got := parseByteSize(tc.in)
		if got != tc.want {
			t.Errorf("parseByteSize(%q): got %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSessionCap_RollingWindow(t *testing.T) {
	// SetCap directly on the session (mirrors what server.applyCaps does
	// after Store.Create). Write past the cap; history should hold the tail.
	dir := filepath.Join(t.TempDir(), "store")
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess, _ := st.Create(store.RunMeta{Name: "capped", Mode: "pipe"})
	sess.SetCap(1024)

	// Three 1 KB writes; first is filler 'F', last two are 'A'.
	filler := make([]byte, 1024)
	for i := range filler {
		filler[i] = 'F'
	}
	tail := make([]byte, 1024)
	for i := range tail {
		tail[i] = 'A'
	}
	sess.Write(filler)
	sess.Write(tail)
	sess.Write(tail)

	hist := sess.History()
	if int64(len(hist)) > 1024 {
		t.Fatalf("history exceeded cap: got %d bytes, want ≤ 1024", len(hist))
	}
	// All bytes should be 'A' (the most recent writes; filler rolled off).
	for _, b := range hist {
		if b != 'A' {
			t.Fatalf("unexpected byte after rolling window: %v", b)
		}
	}
	// Total BytesRecv reflects the full volume written, not the in-memory window.
	if sess.BytesRecv != 3*1024 {
		t.Fatalf("BytesRecv: got %d, want %d", sess.BytesRecv, 3*1024)
	}
}

func TestSessionCap_Unlimited(t *testing.T) {
	// No SetCap → history grows unbounded.
	dir := filepath.Join(t.TempDir(), "store")
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess, _ := st.Create(store.RunMeta{Name: "big", Mode: "pipe"})
	chunk := make([]byte, 4096)
	sess.Write(chunk)
	sess.Write(chunk)

	if int64(len(sess.History())) != 8192 {
		t.Fatalf("expected unbounded history, got %d", len(sess.History()))
	}
}

func TestApplyCaps_Integration(t *testing.T) {
	// End-to-end: SPOUT_SESSION_CAP set; server applies caps to a session
	// pre-created via POST /api/run; subsequent writes respect the cap.
	t.Setenv("SPOUT_SESSION_CAP", "1K")
	app, st := newTestApp(t, nil)

	// Pre-create the run via REST (which runs through applyCaps).
	body := strings.NewReader(`{"name":"capped","mode":"pipe"}`)
	req, _ := http.NewRequest("POST", "/api/run", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != 200 {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST status %d, body %q", resp.StatusCode, buf)
	}

	sess := st.Get("capped")
	if sess == nil {
		t.Fatal("session not created")
	}
	// Write past the cap and verify rolling.
	chunk := make([]byte, 1024)
	for i := range chunk {
		chunk[i] = 'X'
	}
	sess.Write(chunk)
	sess.Write(chunk)
	sess.Write(chunk)

	if got := int64(len(sess.History())); got > 1024 {
		t.Fatalf("history exceeded cap after applyCaps: %d", got)
	}
}

func TestDownload_Header(t *testing.T) {
	app, st := newTestApp(t, nil)
	sess, _ := st.Create(store.RunMeta{Name: "logged", Mode: "pipe"})
	sess.Write([]byte("epoch 1: loss=0.5\n"))

	// Without ?download → no Content-Disposition.
	resp := mustGet(t, app, "/api/run/logged/raw", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("raw: %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		t.Fatalf("expected no Content-Disposition, got %q", cd)
	}

	// With ?download=1 → attachment header set.
	resp = mustGet(t, app, "/api/run/logged/raw?download=1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("raw download: %d", resp.StatusCode)
	}
	cd := resp.Header.Get("Content-Disposition")
	want := `attachment; filename="logged.log"`
	if cd != want {
		t.Fatalf("Content-Disposition: got %q, want %q", cd, want)
	}
}

func TestEffectiveReplayCap(t *testing.T) {
	// Default when unset.
	t.Setenv("SPOUT_REPLAY_CAP", "")
	c := loadCapsConfig()
	if got := c.effectiveReplayCap(); got != defaultReplayCap {
		t.Fatalf("default: %d, want %d", got, defaultReplayCap)
	}

	// Override.
	t.Setenv("SPOUT_REPLAY_CAP", "1MB")
	c = loadCapsConfig()
	if got := c.effectiveReplayCap(); got != 1<<20 {
		t.Fatalf("override: %d, want %d", got, 1<<20)
	}
}
