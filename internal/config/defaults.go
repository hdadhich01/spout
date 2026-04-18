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

	// DefaultStorageSubdir is the folder (under the spout config dir) where
	// the server and the CLI archive keep per-session `meta.json` +
	// `data.raw`. The yaml `storage:` field overrides this when set.
	DefaultStorageSubdir = "storage"

	// LegacyStorageSubdir is the previous name for DefaultStorageSubdir.
	// Checked on startup so existing users' history gets auto-migrated.
	LegacyStorageSubdir = "sessions"
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

// DefaultStorageDir returns the storage folder path. If the legacy
// `sessions/` folder exists and the new `storage/` folder does not, it
// renames sessions → storage in place so existing history survives the
// rename. Either way the return value is the canonical path.
func DefaultStorageDir() string {
	dir := filepath.Join(ConfigDir(), DefaultStorageSubdir)
	legacy := filepath.Join(ConfigDir(), LegacyStorageSubdir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := os.Stat(legacy); err == nil {
			os.Rename(legacy, dir) // best-effort; a failure is logged elsewhere
		}
	}
	return dir
}
