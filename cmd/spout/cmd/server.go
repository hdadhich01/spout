package cmd

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/server"
	"github.com/hdadhich01/spout/internal/store"
	"github.com/spf13/cobra"
)

var serverPort string

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start a local dashboard server",
	Long: `Starts the spout dashboard server.

  spout server
  spout server -p 8080`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Warn if stdout is piped.
		if stat, _ := os.Stdout.Stat(); stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
			warn("stdout %s - did you mean to run 'spout server' on its own?", cYellow("is piped"))
		}

		// Check port before starting. Offer to use next free port if taken.
		if err := checkPort(serverPort); err != nil {
			fail("port %s %s", cBold(serverPort), cRed("is in use"))
			next := findFreePort(serverPort)
			if next != "" {
				if confirmYes(fmt.Sprintf("use port %s instead?", cBold(next))) {
					serverPort = next
				} else {
					return nil // graceful exit, error already printed
				}
			} else {
				return nil
			}
		}

		dir := config.DefaultStorageDir()
		st, err := store.New(dir)
		if err != nil {
			return fmt.Errorf("opening run store: %w", err)
		}

		banner()
		fmt.Fprintf(stderr, "\n")
		label("listen", cAqua("http://localhost:"+serverPort))
		label("storage", cDim(shortPath(dir)))
		label("runs", fmt.Sprintf("%d loaded", len(st.List())))
		fmt.Fprintf(stderr, "\n")

		app := server.New(st)
		if err := app.Listen(":"+serverPort, fiber.ListenConfig{
			DisableStartupMessage: true,
		}); err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
		return nil
	},
}

func init() {
	serverCmd.Flags().StringVarP(&serverPort, "port", "p", config.DefaultLocalPort, "port to listen on")
	serverCmd.GroupID = groupServer
	rootCmd.AddCommand(serverCmd)
}

func checkPort(port string) error {
	conn, err := net.DialTimeout("tcp", "localhost:"+port, 500*time.Millisecond)
	if err != nil {
		return nil // port is free
	}
	conn.Close()
	return fmt.Errorf("port %s is already in use", cBold(port))
}

// findFreePort returns the next free port after the given starting port.
// Tries up to 20 ports. Returns "" if none found.
func findFreePort(start string) string {
	var n int
	if _, err := fmt.Sscanf(start, "%d", &n); err != nil {
		return ""
	}
	for i := 1; i <= 20; i++ {
		p := fmt.Sprintf("%d", n+i)
		if checkPort(p) == nil {
			return p
		}
	}
	return ""
}
