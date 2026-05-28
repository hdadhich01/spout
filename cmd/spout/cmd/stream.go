package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

var (
	streamSession string
	streamCmd_    string
)

// idleTimeout is how long a run-mode tmux session is allowed to sit at an
// awaiting prompt after its command has finished before _stream kills it.
// Gives the user some room to come back and inspect output without letting
// dead sessions hang around forever.
const idleTimeout = 10 * time.Minute

// _stream is an internal command invoked by tmux pipe-pane.
var streamCmd = &cobra.Command{
	Use:    "_stream",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		go func() {
			<-sigCh
			cancel()
		}()

		addr, _ := resolveServer()
		tokenVar := resolveServerTokenVar()

		// Watch pane bytes in-band: keep a local copy of HasExit / last-byte
		// time so the watchdog doesn't have to round-trip to the server.
		var hasExit atomic.Bool
		var lastByte atomic.Int64
		lastByte.Store(time.Now().UnixNano())

		r := &watchReader{
			r: os.Stdin,
			onByte: func(data []byte) {
				lastByte.Store(time.Now().UnixNano())
				if !hasExit.Load() && bytes.Contains(data, []byte("\x1b]9999;")) {
					hasExit.Store(true)
				}
			},
		}

		// Idle watchdog: once the exit marker has been seen, count idle time
		// and kill tmux after idleTimeout so the session moves to a final
		// state (success/error) instead of hanging on the shell prompt.
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-t.C:
					if !hasExit.Load() {
						continue
					}
					last := time.Unix(0, lastByte.Load())
					if now.Sub(last) >= idleTimeout {
						exec.Command("tmux", "kill-session", "-t", streamSession).Run()
						return
					}
				}
			}
		}()

		_, err := streamToSession(ctx, r, addr, tokenVar, streamSession, "run", streamCmd_)
		if err != nil && !errors.Is(err, errInterrupted) {
			return fmt.Errorf("streaming %s: %w", streamSession, err)
		}
		return nil
	},
}

// watchReader wraps an io.Reader and invokes onByte with each chunk read.
type watchReader struct {
	r      io.Reader
	onByte func([]byte)
}

func (w *watchReader) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 && w.onByte != nil {
		w.onByte(p[:n])
	}
	return n, err
}

func init() {
	streamCmd.Flags().StringVar(&streamSession, "session", "", "session name")
	streamCmd.Flags().StringVar(&streamCmd_, "cmd", "", "command string")
	rootCmd.AddCommand(streamCmd)
}
