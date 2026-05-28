package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/names"
	"github.com/hdadhich01/spout/internal/observe"
	"github.com/hdadhich01/spout/internal/store"
	"github.com/spf13/cobra"
)

var (
	serverAddr   string
	sessionName  string
	localMode    bool
	observeOn    bool   // --observe: force observability on for this run
	observeOff   bool   // --no-observe: force it off (e.g. a sensitive run)
	otelEndpoint string // --otel: also export events as OTLP spans
	stderr       io.Writer = os.Stderr
)

// errInterrupted signals a user-initiated stop (Ctrl-C): a clean exit, not a
// failure. streamToSession returns it; callers report a "stopped" + exit 0.
var errInterrupted = errors.New("interrupted")

var rootCmd = &cobra.Command{
	Use:           "spout",
	Short:         "Pipe any command into a live web dashboard",
	Long:          "Spout streams terminal output to a live web dashboard.",
	RunE:          runPipe,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Command group IDs for help organization.
const (
	groupStream  = "streaming"
	groupSession = "sessions"
	groupServer  = "server"
	groupSetup   = "setup"
)

func Execute() error {
	// Best-effort: populate the global config with the annotated template
	// if it's missing or empty. No-op on existing files so we never clobber
	// user edits. Failures are silent (permission issues etc.) - every
	// callsite that actually needs config handles the absent case.
	config.EnsureSystemConfig()
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&serverAddr, "server", "s", "", "server profile or host:port")
	rootCmd.PersistentFlags().BoolVarP(&localMode, "local", "l", false, "use "+config.DefaultLocalAddr())
	rootCmd.PersistentFlags().StringVarP(&sessionName, "name", "n", "", "name for the run/stream")
	rootCmd.PersistentFlags().BoolVar(&observeOn, "observe", false, "force observability on for this run")
	rootCmd.PersistentFlags().BoolVar(&observeOff, "no-observe", false, "disable observability for this run")
	rootCmd.PersistentFlags().StringVar(&otelEndpoint, "otel", "", "also export observe events as OTLP/HTTP spans to this endpoint")

	rootCmd.AddGroup(
		&cobra.Group{ID: groupStream, Title: "streaming"},
		&cobra.Group{ID: groupSession, Title: "runs"},
		&cobra.Group{ID: groupServer, Title: "server"},
		&cobra.Group{ID: groupSetup, Title: "setup"},
	)

	rootCmd.SetHelpFunc(customHelp)
}

// observeEnabled decides whether the observer runs for this run. Config sets
// the baseline (observe.enabled); --observe / --no-observe override it, with
// --no-observe winning a conflict (fail safe / private).
func observeEnabled(cfg config.Config) bool {
	enabled := cfg.Observe != nil && cfg.Observe.Enabled
	if observeOn {
		enabled = true
	}
	if observeOff {
		enabled = false
	}
	return enabled
}

// otelTarget resolves the OTLP endpoint: --otel flag wins, else observe.otel.
func otelTarget(o *config.Observe) string {
	if otelEndpoint != "" {
		return otelEndpoint
	}
	if o != nil {
		return o.OTel
	}
	return ""
}

// resolveServer returns the server URL and token based on flags and config.
// Priority: --local > --server flag > config default
func resolveServer() (addr, token string) {
	if localMode {
		return config.DefaultLocalAddr(), ""
	}
	cfg := config.Load()
	return cfg.Resolve(serverAddr)
}

// resolveServerTokenVar returns the env-var NAME the resolved profile reads
// its token from (or "" for tokenless / raw host:port / --local). Used by
// the CLI's local copy to record server identity in run meta without ever
// serializing the secret value.
func resolveServerTokenVar() string {
	if localMode {
		return ""
	}
	cfg := config.Load()
	profile := cfg.ResolvedProfile(serverAddr)
	if profile == "" {
		return ""
	}
	return cfg.TokenVarFor(profile)
}

// addServerAuth sets a Bearer header from the resolved server profile's token
// env var, when one is set. No-op for tokenless / same-box (loopback) servers.
func addServerAuth(req *http.Request) {
	if v := os.Getenv(resolveServerTokenVar()); v != "" {
		req.Header.Set("Authorization", "Bearer "+v)
	}
}

func runPipe(cmd *cobra.Command, args []string) error {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		// TTY stdin, no pipe. Bare `spout` never launches anything -
		// it's always a read-only status view. The blueprint launcher
		// lives at `spout run`, which checks cfg.Streams itself.
		return showStatus(cmd)
	}

	userChoseName := sessionName != ""
	if sessionName == "" {
		sessionName = names.Generate()
	}

	addr, _ := resolveServer()
	if err := checkServer(addr); err != nil {
		return err
	}

	// Don't create a server session until we've actually seen bytes from
	// stdin. If the upstream command dies before producing output (typo'd
	// script path, missing binary, etc.), we just exit without polluting
	// the dashboard with an empty ghost run.
	first, err := peekStdin(os.Stdin)
	if err != nil {
		fail("%s - upstream command produced no output", cRed("nothing to stream"))
		return ErrAlreadyReported
	}

	if err := resolveSessionName(addr, &sessionName, userChoseName, false); err != nil {
		return err
	}
	runURL := "http://" + addr + "/r/" + sessionName
	ok("%s run %s", cGreen("started"), cBold(sessionName))
	info("dashboard at %s", cAqua(runURL))
	copyToClipboard(runURL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cancel()
	}()

	// Prepend the byte(s) we peeked so they're not lost.
	r := io.MultiReader(newBufReader(first), os.Stdin)
	tokenVar := resolveServerTokenVar()
	// Best-effort: identify the upstream command of `cmd | spout` so the
	// dashboard title shows it, matching run-mode UX. Linux-only via /proc.
	upstreamCmd := captureUpstreamCommand()
	bytesSent, err := streamToSession(ctx, r, addr, tokenVar, sessionName, "pipe", upstreamCmd)
	switch {
	case errors.Is(err, errInterrupted):
		ok("%s - %s sent", cGreen("stopped"), humanBytes(bytesSent))
		return nil
	case err != nil:
		fail("%s: %v", cRed("stream failed"), err)
		return ErrAlreadyReported
	}
	ok("%s - %s sent", cGreen("done"), humanBytes(bytesSent))
	return nil
}

// peekStdin blocks until at least one byte is available on stdin, or EOF.
// Returns the peeked byte(s) so the caller can prepend them to the stream.
// Returns an error if EOF is reached with zero bytes.
func peekStdin(f *os.File) ([]byte, error) {
	buf := make([]byte, 1)
	n, err := f.Read(buf)
	if n == 0 {
		if err == nil {
			err = io.EOF
		}
		return nil, err
	}
	return buf[:n], nil
}

func newBufReader(b []byte) io.Reader {
	return &bytesReader{b: b}
}

type bytesReader struct {
	b []byte
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

// streamToSession reads raw bytes from r and sends them to the server
// for the given session name, while dual-writing every byte to the user's
// local copy (the canonical store — see ARCHITECTURE.md §2). Returns
// total bytes sent over the wire.
//
// `tokenVar` is the env-var NAME of the auth token for the resolved server
// profile (or "" for tokenless). It's recorded on the local Session's
// metadata so later commands (delete/rename/share) can target the same
// server even after `default_server` changes — without ever serializing
// the secret value.
//
// Local write failures warn and continue (server-only fallback). Server-
// side write failures abort the run; the local copy retains every byte
// received up to that point.
func streamToSession(ctx context.Context, r io.Reader, addr, tokenVar, session, mode, command string) (int64, error) {
	m := collectMeta()
	cfg := config.Load()

	// Dual-write target: open the local store and mint a Session that
	// mirrors the run. If this fails we keep going server-only.
	local, localErr := store.New(cfg.ResolveLocal())
	var localSess *store.Session
	if localErr != nil {
		warn("local copy: %v (continuing server-only)", localErr)
	} else {
		ls, err := local.Create(store.RunMeta{
			Name:        session,
			Mode:        mode,
			Command:     command,
			Dir:         m.Dir,
			Host:        m.Host,
			User:        m.User,
			GitBranch:   m.GitBranch,
			GitCommit:   m.GitCommit,
			ServerURL:   addr,
			ServerToken: tokenVar,
		})
		if err != nil {
			warn("local copy: %v (continuing server-only)", err)
		} else {
			localSess = ls
			defer func() {
				localSess.Close()
				local.SaveMeta(localSess)
			}()
		}
	}

	// Observability: a CLI-side loop that watches this run's plaintext output
	// and emits structured events. Opt-in via `observe:` in spout.yaml; the
	// --observe / --no-observe flags override per run; nil (and a no-op) when
	// disabled or when there's no local folder to write to.
	var obs *observe.Engine
	if localSess != nil && observeEnabled(cfg) {
		oc := cfg.Observe
		if oc == nil {
			oc = &config.Observe{}
		}
		eff := *oc        // shallow copy; flags decide enablement
		eff.Enabled = true

		evPath := filepath.Join(local.SessionDir(session), "events.jsonl")
		if sink, err := observe.NewFileSink(evPath); err != nil {
			warn("observe: %v (disabled for this run)", err)
		} else {
			// Local file is canonical; the server sink is a live convenience
			// layer (best-effort) so the dashboard can render events.
			sinks := []observe.Sink{sink, observe.NewServerSink(addr, session, tokenVar)}
			if ep := otelTarget(oc); ep != "" {
				sinks = append(sinks, observe.NewOTelSink(ep, session))
			}
			obs = observe.New(observe.RunConfig{
				Run:     session,
				Command: command,
				Mode:    mode,
				Dir:     m.Dir,
				Observe: &eff,
			}, sinks...)
			defer obs.Close()
		}
	}

	q := url.Values{}
	q.Set("mode", mode)
	if command != "" {
		q.Set("cmd", command)
	}
	if m.Dir != "" {
		q.Set("dir", m.Dir)
	}
	if m.Host != "" {
		q.Set("host", m.Host)
	}
	if m.User != "" {
		q.Set("user", m.User)
	}
	if m.GitBranch != "" {
		q.Set("git_branch", m.GitBranch)
	}
	if m.GitCommit != "" {
		q.Set("git_commit", m.GitCommit)
	}
	wsURL := fmt.Sprintf("ws://%s/ingest/%s?%s", addr, session, q.Encode())

	// Authenticate the ingest connection on token-gated servers (Tier 3).
	// tokenVar names the env var holding the value; same-box servers don't
	// require it (loopback is trusted) but sending it is harmless.
	var dialOpts *websocket.DialOptions
	if tokenVar != "" {
		if tok := os.Getenv(tokenVar); tok != "" {
			dialOpts = &websocket.DialOptions{
				HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
			}
		}
	}
	conn, _, err := websocket.Dial(ctx, wsURL, dialOpts)
	if err != nil {
		return 0, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)

	buf := make([]byte, 64*1024)
	var batch []byte
	var totalSent int64

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		// Local first: bytes survive even if the WS write fails next.
		if localSess != nil {
			localSess.Write(batch)
		}
		err := conn.Write(ctx, websocket.MessageBinary, batch)
		totalSent += int64(len(batch))
		batch = batch[:0]
		return err
	}

	chunks := make(chan []byte, 64)
	readErr := make(chan error, 1)
	go func() {
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				chunks <- chunk
			}
			if err != nil {
				close(chunks)
				if err == io.EOF {
					readErr <- nil
				} else {
					readErr <- err
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// User interrupted (Ctrl-C). Best-effort flush the tail on a
			// fresh deadline (the run ctx is already cancelled), close the
			// connection cleanly, and signal a stop rather than a failure.
			if len(batch) > 0 {
				if localSess != nil {
					localSess.Write(batch)
				}
				fctx, fcancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := conn.Write(fctx, websocket.MessageBinary, batch); err == nil {
					totalSent += int64(len(batch))
				}
				fcancel()
			}
			conn.Close(websocket.StatusNormalClosure, "interrupted")
			return totalSent, errInterrupted
		case chunk, ok := <-chunks:
			if !ok {
				if err := flush(); err != nil {
					return totalSent, err
				}
				conn.Close(websocket.StatusNormalClosure, "done")
				return totalSent, <-readErr
			}
			os.Stdout.Write(chunk)
			obs.Feed(chunk)
			batch = append(batch, chunk...)
			if len(batch) >= 64*1024 {
				if err := flush(); err != nil {
					return totalSent, err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return totalSent, err
			}
		}
	}
}

func humanBytes(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	// Unit attached to the number, no space (17B, 2.9KB).
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1fTB", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// humanDuration formats a duration in compound units. No decimals, no seconds.
//   <1m   → "<1m"
//   1-59m → "30m"
//   1-23h → "1h30m" (or "1h" if no leftover minutes)
//   24h+  → "1d12h" (days + hours, no minutes)
func humanDuration(ms int64) string {
	m := ms / 60000 // total minutes
	if m < 1 {
		return "<1m"
	}
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if m < 24*60 {
		h := m / 60
		mins := m % 60
		if mins == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, mins)
	}
	d := m / (24 * 60)
	h := (m % (24 * 60)) / 60
	if h == 0 {
		return fmt.Sprintf("%dd", d)
	}
	return fmt.Sprintf("%dd%dh", d, h)
}
