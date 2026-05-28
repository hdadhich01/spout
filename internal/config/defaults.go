package config

import (
	"os"
	"path/filepath"
)

// Default values used when no config exists. Every CLI/server reference to a
// hostname, port, or system path should come from this file - never
// hardcoded.
const (
	// DefaultRemoteHost is the public hosted spout server.
	DefaultRemoteHost = "spout.sh"

	// DefaultLocalHost is the loopback host used by `spout server` and `-l`.
	DefaultLocalHost = "localhost"

	// DefaultLocalPort is the listening port for the local server.
	DefaultLocalPort = "3000"

	// DefaultServerSubdir is the folder under ~/.spout where the server
	// keeps per-session `meta.json` + `data.raw`. The yaml `storage:`
	// field overrides this when set.
	DefaultServerSubdir = "server"

	// DefaultLocalSubdir is the folder under ~/.spout where the CLI keeps
	// every byte of every run as the canonical local copy. The yaml
	// `local:` field overrides this when set. Sibling of DefaultServerSubdir
	// so a colocated install reads as `~/.spout/{local,server}/` — same
	// parent, distinct stores, never collide.
	DefaultLocalSubdir = "local"

	// --- observability defaults (see config.Observe) ---

	// DefaultObserveInterval is the fallback poll cadence for the observer
	// loop when neither the run nor a rule sets its own.
	DefaultObserveInterval = "60s"

	// DefaultObserveModelType is the backing-LLM kind chosen when
	// observe.model.type is unset. "api" = a cloud provider (Anthropic).
	DefaultObserveModelType = "api"

	// DefaultObserveModelID is the model used when observe.model.model is
	// unset and type=api.
	DefaultObserveModelID = "claude-haiku-4-5"

	// DefaultObserveEndpoint is the local-model endpoint assumed when
	// type=local and no endpoint is given (Ollama's default).
	DefaultObserveEndpoint = "http://localhost:11434"

	// DefaultObserveTokenVar is the env var the observer reads its API key
	// from when type=api and observe.model.token names no other var. Matches
	// the Anthropic SDK's own default so dev needs zero config.
	DefaultObserveTokenVar = "ANTHROPIC_API_KEY"

	// --- terminal sizing (tmux + xterm) ---
	//
	// The two have to match exactly: programs (claude, k9s, vim, …) render
	// for the tmux pane's columns, and xterm's redraw of those bytes only
	// looks right at the same width. Picked 120×30 because:
	//   - wide enough for modern TUIs (claude/k9s comfort zone)
	//   - narrow enough to fit a ~1100px content column without scrolling
	//   - 30 rows is a sensible "above the fold" viewport (scrollback holds
	//     the rest)
	DefaultTerminalCols = "120"
	DefaultTerminalRows = "30"
)

// DefaultLocalAddr returns "localhost:3000" - the address used by `-l` and
// the fallback for `findRunServer`.
func DefaultLocalAddr() string {
	return DefaultLocalHost + ":" + DefaultLocalPort
}

// ConfigDir returns the spout config directory, preferring the platform
// UserConfigDir and falling back to ~/.config/spout.
func ConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout")
}

// TokensPath returns the server's ingest-token store
// (`~/.config/spout/tokens.json`, overridable with $SPOUT_TOKENS). Shared by
// `spout server` and the `spout server token` CLI so both agree on one
// location.
func TokensPath() string {
	if p := os.Getenv("SPOUT_TOKENS"); p != "" {
		return p
	}
	return filepath.Join(ConfigDir(), "tokens.json")
}

// SpoutRoot returns ~/.spout — the parent directory for every byte
// spout writes (both CLI-local and server data dirs live underneath).
func SpoutRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spout")
}

// DefaultStorageDir returns the server's data folder (`~/.spout/server/`
// by default). On first call, opportunistically migrates from older
// locations so existing data survives the relocation:
//
//   - `~/.config/spout/storage/` (Apr 15 default — when this slice ships)
//   - `~/.config/spout/sessions/` (pre-Apr 15)
//
// Best-effort renames; a failure is logged elsewhere. Returns the
// canonical path either way.
func DefaultStorageDir() string {
	dir := filepath.Join(SpoutRoot(), DefaultServerSubdir)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	// Try the prior-default location first, then the older one.
	for _, legacy := range []string{
		filepath.Join(ConfigDir(), "storage"),
		filepath.Join(ConfigDir(), "sessions"),
	} {
		if _, err := os.Stat(legacy); err == nil {
			os.MkdirAll(filepath.Dir(dir), 0755)
			os.Rename(legacy, dir)
			break
		}
	}
	return dir
}

// DefaultLocalDir returns the CLI's local-copy folder (`~/.spout/local/`
// by default). Auto-migrates from `~/.spout/runs/` (its name in the
// first dual-write slice) the same way DefaultStorageDir does.
func DefaultLocalDir() string {
	dir := filepath.Join(SpoutRoot(), DefaultLocalSubdir)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	legacy := filepath.Join(SpoutRoot(), "runs")
	if _, err := os.Stat(legacy); err == nil {
		os.MkdirAll(filepath.Dir(dir), 0755)
		os.Rename(legacy, dir)
	}
	return dir
}
