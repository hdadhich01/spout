package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check your spout environment",
	Long: `Run a series of checks to diagnose your spout installation.

  spout doctor`,
	RunE: func(cmd *cobra.Command, args []string) error {
		section("checks")

		// 1. tmux installed
		if _, err := exec.LookPath("tmux"); err == nil {
			pass("tmux", cGreen("found"))
		} else {
			fail2("tmux", cRed("not found")+" - `spout run` won't work")
		}

		// 2. clipboard tool
		clipTool := detectClipboard()
		if clipTool != "" {
			pass("clipboard", clipTool+" "+cGreen("found"))
		} else {
			warn2("clipboard", cYellow("no tool found")+" - auto-copy disabled")
		}

		// 3. browser opener
		opener := detectBrowserOpener()
		if opener != "" {
			pass("browser", opener+" "+cGreen("found"))
		} else {
			warn2("browser", cYellow("no opener found")+" - `spout open` won't work")
		}

		// 4. config files readable
		sysPath := config.SystemPath()
		if _, err := os.Stat(sysPath); err == nil {
			pass("config", cAqua(shortPath(sysPath)))
		} else if os.IsNotExist(err) {
			warn2("config", cYellow("no system config")+" (run `spout login` to create one)")
		} else {
			fail2("config", cRed(err.Error()))
		}

		// 5. server reachable
		addr, _ := resolveServer()
		ok, reachable, authRequired, code := probeSpout(addr)
		switch {
		case !reachable:
			fail2("server", fmt.Sprintf("%s - %s", addr, cRed("unreachable")))
		case authRequired:
			warn2("server", fmt.Sprintf("%s - %s", addr, cYellow("auth required")))
		case ok:
			pass("server", fmt.Sprintf("%s - %s", addr, cGreen("spout-compatible")))
		case code == 200:
			fail2("server", fmt.Sprintf("%s - %s", addr, cRed("not spout-compatible")))
		default:
			warn2("server", fmt.Sprintf("%s - %s %d", addr, cYellow("status"), code))
		}

		fmt.Fprintf(stderr, "\n")
		return nil
	},
}

// pass / warn2 / fail2 print a check result with a colored mark.
// (warn2/fail2 to avoid colliding with existing warn/fail helpers.)
// pass / warn2 / fail2 share the same layout. If the value already contains
// ANSI escapes the caller has done its own coloring and we print as-is;
// otherwise the whole value gets the severity hue.
func checkRow(mark, name, value string, hue func(string) string) {
	if !strings.Contains(value, "\x1b[") {
		value = hue(value)
	}
	fmt.Fprintf(stderr, "  %s %s  %s\n", mark, cAqua(cBold(rpad(name, labelWidth))), value)
}

func pass(name, value string)  { checkRow(cGreen("✓"), name, value, cGreen) }
func warn2(name, value string) { checkRow(cYellow("!"), name, value, cYellow) }
func fail2(name, value string) { checkRow(cRed("✗"), name, value, cRed) }

func rpad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	pad := ""
	for i := 0; i < n-len(s); i++ {
		pad += " "
	}
	return s + pad
}

func detectClipboard() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pbcopy"); err == nil {
			return "pbcopy"
		}
	case "windows":
		if _, err := exec.LookPath("clip"); err == nil {
			return "clip"
		}
	default:
		for _, t := range []string{"xclip", "xsel", "wl-copy"} {
			if _, err := exec.LookPath(t); err == nil {
				return t
			}
		}
	}
	return ""
}

func detectBrowserOpener() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return "xdg-open"
		}
	}
	return ""
}

func init() {
	doctorCmd.GroupID = groupSetup
	rootCmd.AddCommand(doctorCmd)
}
