package server

import (
	"os"
	"strconv"
	"strings"
)

// defaultReplayCap is the initial-replay window for new viewer connections.
// xterm.js gets sluggish past tens of MB; the browser shouldn't try to render
// huge histories. Older bytes are still accessible via `?download=1`.
const defaultReplayCap = 4 * 1024 * 1024 // 4 MB

// capsConfig captures size/rate caps at server boot. Reads env once.
//
//	SPOUT_SESSION_CAP — per-session in-memory cap (e.g., "100MB", "1GB").
//	                    0 / unset = unlimited (Tier 3 self-host default).
//	                    Hosted mode would set this; M1 leaves it off.
//	SPOUT_REPLAY_CAP  — initial-replay window per viewer connect.
//	                    Default 4 MB. 0 = unlimited (don't cap; risky for
//	                    huge logs since xterm.js will choke).
type capsConfig struct {
	sessionCap int64 // bytes; 0 = unlimited
	replayCap  int64 // bytes; 0 = use defaultReplayCap
}

func loadCapsConfig() capsConfig {
	return capsConfig{
		sessionCap: parseByteSize(os.Getenv("SPOUT_SESSION_CAP")),
		replayCap:  parseByteSize(os.Getenv("SPOUT_REPLAY_CAP")),
	}
}

// effectiveReplayCap returns the operative replay cap: env override or default.
func (c capsConfig) effectiveReplayCap() int64 {
	if c.replayCap > 0 {
		return c.replayCap
	}
	return defaultReplayCap
}

// parseByteSize accepts forms like "100MB", "1GB", "500K", "12345".
// Returns 0 for empty / unparseable input — that's fine, 0 means "unlimited".
func parseByteSize(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "TB"):
		multiplier = 1 << 40
		s = strings.TrimSuffix(s, "TB")
	case strings.HasSuffix(s, "GB"):
		multiplier = 1 << 30
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		multiplier = 1 << 20
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		multiplier = 1 << 10
		s = strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "T"):
		multiplier = 1 << 40
		s = strings.TrimSuffix(s, "T")
	case strings.HasSuffix(s, "G"):
		multiplier = 1 << 30
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		multiplier = 1 << 20
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		multiplier = 1 << 10
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n * multiplier
}
