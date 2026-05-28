package observe

import (
	"hash/fnv"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Signals is the cheap, local pre-filter snapshot. It both feeds the model
// (so it has context for free) and gates whether a check is worth an LLM call
// at all — the loop only spends a call when something changed or looks off.
type Signals struct {
	TotalBytes   int64
	BytesPerSec  float64
	RepeatedLine bool // same line ≥ repeatThreshold times in a row
	ErrorKeyword bool // an error-ish keyword appeared since the last check
	Idle         bool // no new bytes for longer than idleAfter
	Dirty        bool // new bytes arrived since the last check
}

const repeatThreshold = 3

var (
	// Strip ANSI CSI + OSC sequences. `.` doesn't span newlines, so OSC
	// matching stays within a line.
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

	// Error-ish keywords (lowercased substring match on ANSI-stripped lines).
	errKeywords = []string{
		"error", "panic", "exception", "traceback", "fatal",
		" nan", "oom", "killed", "segfault", "assertionerror",
	}
)

// stripANSI removes escape sequences; safe on multi-line input.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// prefilter accumulates cheap signals from the byte stream. All state is
// mutex-guarded so feed (from the engine loop) and snapshot/trigger reads
// stay consistent.
type prefilter struct {
	mu sync.Mutex

	total      int64
	sinceCheck int64
	lastByte   time.Time

	winStart time.Time
	winBytes int64
	rate     float64

	line      strings.Builder
	lastHash  uint64
	repeatRun int
	repeated  bool
	errKw     bool

	idleAfter time.Duration
	idleFired bool
}

func newPrefilter(idleAfter time.Duration) *prefilter {
	now := time.Now()
	return &prefilter{lastByte: now, winStart: now, idleAfter: idleAfter}
}

// feed ingests a chunk of raw output.
func (p *prefilter) feed(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	n := int64(len(b))
	p.total += n
	p.sinceCheck += n
	p.lastByte = now
	p.idleFired = false

	// Rolling ~1s velocity window.
	p.winBytes += n
	if d := now.Sub(p.winStart); d >= time.Second {
		p.rate = float64(p.winBytes) / d.Seconds()
		p.winBytes = 0
		p.winStart = now
	}

	for _, c := range b {
		if c == '\n' {
			p.finishLine()
			continue
		}
		p.line.WriteByte(c)
	}
}

func (p *prefilter) finishLine() {
	raw := p.line.String()
	p.line.Reset()

	clean := stripANSI(raw)
	if i := strings.LastIndex(clean, "\r"); i >= 0 {
		clean = clean[i+1:] // progress-bar spam: keep last segment
	}
	clean = strings.ToLower(strings.TrimSpace(clean))
	if clean == "" {
		return
	}

	for _, kw := range errKeywords {
		if strings.Contains(clean, kw) {
			p.errKw = true
			break
		}
	}

	h := fnv.New64a()
	h.Write([]byte(clean))
	sum := h.Sum64()
	if sum == p.lastHash {
		p.repeatRun++
		if p.repeatRun >= repeatThreshold {
			p.repeated = true
		}
	} else {
		p.lastHash = sum
		p.repeatRun = 1
	}
}

// trigger reports a reason to preempt the interval timer, or "" to wait.
// Idle fires at most once per quiet period.
func (p *prefilter) trigger() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.errKw:
		return "error"
	case p.repeated:
		return "loop"
	case p.idleAfter > 0 && !p.idleFired && time.Since(p.lastByte) > p.idleAfter:
		p.idleFired = true
		return "idle"
	}
	return ""
}

// snapshot returns current signals without resetting per-interval flags.
func (p *prefilter) snapshot() Signals {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Signals{
		TotalBytes:   p.total,
		BytesPerSec:  p.rate,
		RepeatedLine: p.repeated,
		ErrorKeyword: p.errKw,
		Idle:         p.idleAfter > 0 && time.Since(p.lastByte) > p.idleAfter,
		Dirty:        p.sinceCheck > 0,
	}
}

// consume resets the per-interval sticky state after a check has run.
func (p *prefilter) consume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sinceCheck = 0
	p.errKw = false
	p.repeated = false
}
