package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/coder/websocket"
	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/names"
	"github.com/spf13/cobra"
)

var (
	serverAddr  string
	sessionName string
	localMode   bool
	stderr      io.Writer = os.Stderr
)

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
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&serverAddr, "server", "s", "", "server address or profile name")
	rootCmd.PersistentFlags().BoolVarP(&localMode, "local", "l", false, "use "+config.DefaultLocalAddr())
	rootCmd.PersistentFlags().StringVarP(&sessionName, "name", "n", "", "session name (random if not set)")

	rootCmd.AddGroup(
		&cobra.Group{ID: groupStream, Title: "streaming"},
		&cobra.Group{ID: groupSession, Title: "sessions"},
		&cobra.Group{ID: groupServer, Title: "server"},
		&cobra.Group{ID: groupSetup, Title: "setup"},
	)

	rootCmd.SetHelpFunc(customHelp)
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

func runPipe(cmd *cobra.Command, args []string) error {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
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

	if err := resolveSessionName(addr, &sessionName, userChoseName); err != nil {
		return err
	}
	runURL := "http://" + addr + "/r/" + sessionName
	ok("session %s %s", cBold(names.Prefix(sessionName)), cGreen("started"))
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
	bytesSent, err := streamToSession(ctx, r, addr, sessionName, "pipe", "")
	if err != nil {
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
// for the given session name. Returns total bytes sent.
func streamToSession(ctx context.Context, r io.Reader, addr, session, mode, command string) (int64, error) {
	m := collectMeta()
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
	wsURL := fmt.Sprintf("ws://%s/ingest/%s?%s", addr, session, q.Encode())

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
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
			return totalSent, fmt.Errorf("interrupted")
		case chunk, ok := <-chunks:
			if !ok {
				if err := flush(); err != nil {
					return totalSent, err
				}
				conn.Close(websocket.StatusNormalClosure, "done")
				return totalSent, <-readErr
			}
			os.Stdout.Write(chunk)
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
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
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
