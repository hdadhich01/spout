package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/names"
)

// probeSpout hits /api/health and returns the server's "spout-compatible" state.
// - ok: reachable and returned {"spout": true}
// - reachable: got any HTTP response (even non-spout)
// - authRequired: 401/403
// - statusCode: raw status if reachable
func probeSpout(addr string) (ok, reachable, authRequired bool, statusCode int) {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/health")
	if err != nil {
		return false, false, false, 0
	}
	defer resp.Body.Close()
	reachable = true
	statusCode = resp.StatusCode
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		authRequired = true
		return
	}
	if resp.StatusCode != 200 {
		return
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return
	}
	if v, _ := m["spout"].(bool); v {
		ok = true
	}
	return
}

// checkServer verifies the server is reachable before starting a run.
func checkServer(addr string) error {
	if addr == "" {
		return fmt.Errorf("no server configured\n\n  Use %s for local, or %s to set a server",
			cAqua("spout -l"), cAqua("spout login"))
	}

	ok, reachable, authRequired, _ := probeSpout(addr)
	if !reachable {
		return fmt.Errorf("cannot reach server at %s\n\n  Start a local server:  %s\n  Use a different server: %s",
			cBold(addr), cAqua("spout server"), cAqua("spout -s <server>"))
	}
	if authRequired {
		return fmt.Errorf("authentication failed for %s\n\n  Set a token: %s\n  Or login:    %s",
			cBold(addr), cAqua("SPOUT_TOKEN=... spout"), cAqua("spout login"))
	}
	if !ok {
		return fmt.Errorf("server at %s is not spout-compatible\n\n  Start a local server:  %s\n  Or login to a server:  %s",
			cBold(addr), cAqua("spout server"), cAqua("spout login <name>"))
	}
	return nil
}

// resolveSessionName checks for name collisions with the server.
// If the user chose a name and it exists, prompt to replace or rename.
// If auto-generated, retry until a unique name is found.
func resolveSessionName(addr string, name *string, userChose bool) error {
	if !sessionExists(addr, *name) {
		return nil
	}

	if !userChose {
		// Auto-generated name collided - just regenerate.
		for i := 0; i < 10; i++ {
			*name = names.Generate()
			if !sessionExists(addr, *name) {
				return nil
			}
		}
		return fmt.Errorf("could not generate a unique session name after 10 attempts")
	}

	// User chose the name and it exists - prompt.
	warn("session %s %s", cBold(*name), cYellow("already exists"))
	fmt.Fprintf(stderr, "  %s replace existing  %s rename\n", cAqua(cBold("[r]")), cAqua(cBold("[n]")))
	fmt.Fprintf(stderr, "  > ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	switch input {
	case "r", "replace", "":
		// Delete the existing session and proceed with the same name.
		client := http.Client{Timeout: 5 * time.Second}
		req, _ := http.NewRequest("DELETE", "http://"+addr+"/api/run/"+*name, nil)
		client.Do(req)
		return nil
	case "n", "rename":
		fmt.Fprintf(stderr, "  new name: ")
		newName, _ := reader.ReadString('\n')
		newName = strings.TrimSpace(newName)
		if newName == "" {
			return fmt.Errorf("no name provided")
		}
		*name = newName
		return resolveSessionName(addr, name, true) // re-check
	default:
		return fmt.Errorf("cancelled")
	}
}

func sessionExists(addr, name string) bool {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/api/run/" + name)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	// Must be a JSON response - otherwise it's some random web server
	// returning a 200 HTML page (e.g., spout.sh's landing page).
	ct := resp.Header.Get("Content-Type")
	return strings.Contains(ct, "json")
}

// findRunServer locates which server actually has a run with the given name.
// Tries the resolved server first (CLI flag → config → default), then localhost
// as a fallback. Returns empty addr if not found anywhere.
//
// This means `spout open trout` works whether trout is on spout.sh, localhost,
// or a custom profile, without the user having to remember the -l / -s flag.
func findRunServer(name string) (addr string, found bool) {
	// 1. Try whatever the user explicitly resolved (flag, config, default).
	primary, _ := resolveServer()
	if primary != "" && sessionExists(primary, name) {
		return primary, true
	}

	// 2. Fall back to localhost - common case where the user forgot -l.
	local := config.DefaultLocalAddr()
	if primary != local && sessionExists(local, name) {
		return local, true
	}

	// Not found.
	if primary == "" {
		primary = local
	}
	return primary, false
}

// validateCommand checks for disallowed command patterns like piping spout into itself.
func validateCommand(userCmd string) error {
	lower := strings.ToLower(strings.TrimSpace(userCmd))

	// Block running spout inside spout.
	for _, bad := range []string{"spout server", "spout run", "| spout"} {
		if strings.Contains(lower, bad) {
			return fmt.Errorf("cannot run spout inside spout")
		}
	}

	return nil
}
