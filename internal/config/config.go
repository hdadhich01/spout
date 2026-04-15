package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the merged result of system + project config.
type Config struct {
	// System-level (from ~/.config/spout/config.yaml)
	DefaultServer string            `yaml:"default_server,omitempty"`
	Servers       map[string]Server `yaml:"servers,omitempty"`

	// Can be set at system or project level. Project overrides system.
	ServerRef string `yaml:"server,omitempty"` // profile name or host:port
	Name      string `yaml:"name,omitempty"`   // session name
	Runs      []Run  `yaml:"runs,omitempty"`   // sub-runs for bare `spout`
}

// Server is a named server profile (system config only, URLs only - tokens in .env).
type Server struct {
	URL string `yaml:"url"`
}

// Run is a sub-run definition in project config.
type Run struct {
	Label   string `yaml:"label"`
	Command string `yaml:"command"`
}

// Load reads system config, system .env, then walks up from cwd for project config.
// Merges all layers with project overriding system.
func Load() Config {
	migrateLegacy()
	sys := loadFile(SystemPath())
	proj := loadProjectConfigs()
	cfg := merge(sys, proj)
	loadEnvTokens(&cfg)
	return cfg
}

// LoadedFiles returns the absolute paths of all spout.yaml and .env files
// that get merged for the current cwd. Returned in order of precedence:
// highest (deepest project) first, lowest (system) last.
//
// Used by `spout config` to show what's actually being loaded and in what
// order.
func LoadedFiles() (configs []string, envs []string) {
	migrateLegacy()

	cwd, err := os.Getwd()
	if err == nil {
		ceiling := findCeiling(cwd)

		// Walk up from cwd to ceiling collecting yaml + env files (deepest first).
		dir := cwd
		for {
			for _, name := range []string{"spout.yaml", ".spout.yaml"} {
				p := filepath.Join(dir, name)
				if _, err := os.Stat(p); err == nil {
					configs = append(configs, p)
					break
				}
			}
			envP := filepath.Join(dir, ".env")
			if _, err := os.Stat(envP); err == nil {
				envs = append(envs, envP)
			}
			if dir == ceiling {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// System config is the lowest priority - append last.
	if _, err := os.Stat(SystemPath()); err == nil {
		configs = append(configs, SystemPath())
	}
	sysEnv := filepath.Join(filepath.Dir(SystemPath()), ".env")
	if _, err := os.Stat(sysEnv); err == nil {
		envs = append(envs, sysEnv)
	}
	return
}

// Resolve returns the server URL and token for a given CLI input.
// Priority: CLI flag > project config > system config > default (spout.sh)
func (c Config) Resolve(cliFlag string) (url, token string) {
	input := cliFlag
	if input == "" {
		input = c.ServerRef
	}
	if input == "" {
		input = c.DefaultServer
	}
	if input == "" {
		return DefaultRemoteHost, ""
	}

	// Check if it's a profile name.
	if c.Servers != nil {
		if srv, ok := c.Servers[input]; ok {
			token = resolveToken(input)
			return srv.URL, token
		}
	}

	// Use as-is (raw host:port).
	return input, resolveToken("")
}

// resolveToken gets a token for a profile from env vars.
// Checks SPOUT_TOKEN_<PROFILE> first, falls back to SPOUT_TOKEN.
func resolveToken(profile string) string {
	if profile != "" {
		key := "SPOUT_TOKEN_" + strings.ToUpper(profile)
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return os.Getenv("SPOUT_TOKEN")
}

// loadEnvTokens loads .env files from system dir then walks up from cwd.
// System .env has lowest priority, deepest folder .env has highest.
func loadEnvTokens(cfg *Config) {
	// System .env (lowest priority - loaded first, won't override later ones)
	loadDotenv(filepath.Join(filepath.Dir(SystemPath()), ".env"))

	// Walk up from cwd to project root, collect .env paths
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	ceiling := findCeiling(dir)
	var envPaths []string
	for {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			envPaths = append(envPaths, p)
		}
		if dir == ceiling {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Load from outermost to innermost (so innermost wins)
	for i := len(envPaths) - 1; i >= 0; i-- {
		loadDotenv(envPaths[i])
	}
}

// Exists returns true if a system config file exists on disk.
func Exists() bool {
	_, err := os.Stat(SystemPath())
	return err == nil
}

// Context returns discovery context for the current cwd - useful for
// displaying where the CLI is looking and what kind of project it found.
type Context struct {
	Cwd      string
	Ceiling  string // git root or home dir
	IsGitDir bool
}

// DiscoverContext returns the discovery context for the current cwd.
func DiscoverContext() Context {
	cwd, _ := os.Getwd()
	ceiling := findCeiling(cwd)
	home, _ := os.UserHomeDir()
	return Context{
		Cwd:      cwd,
		Ceiling:  ceiling,
		IsGitDir: ceiling != home && ceiling != "/",
	}
}

// SystemPath returns ~/.config/spout/spout.yaml.
// All config files (system and project) use the same name and schema.
// If a legacy config.yaml exists at this location, migrate it on read.
func SystemPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout", "spout.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout", "spout.yaml")
}

// legacySystemPath is the old config.yaml location, migrated automatically.
func legacySystemPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout", "config.yaml")
}

// migrateLegacy renames the old config.yaml to spout.yaml if needed.
func migrateLegacy() {
	old := legacySystemPath()
	new := SystemPath()
	if old == new {
		return
	}
	if _, err := os.Stat(new); err == nil {
		return // new path already exists, nothing to migrate
	}
	if _, err := os.Stat(old); err == nil {
		os.Rename(old, new)
	}
}

// merge combines system and project configs. Project wins on conflicts.
func merge(sys, proj Config) Config {
	out := sys
	if proj.ServerRef != "" {
		out.ServerRef = proj.ServerRef
	}
	if proj.Name != "" {
		out.Name = proj.Name
	}
	if len(proj.Runs) > 0 {
		out.Runs = proj.Runs // replaces, no merge
	}
	// Merge servers: project profiles override system profiles on name conflicts.
	if len(proj.Servers) > 0 {
		if out.Servers == nil {
			out.Servers = make(map[string]Server)
		}
		for k, v := range proj.Servers {
			out.Servers[k] = v
		}
	}
	return out
}

// loadProjectConfigs walks up from cwd to the project root, collects all
// spout.yaml files, then merges them (deepest wins on conflicts).
//
// Project root is determined by:
// 1. Git root (`git rev-parse --show-toplevel`) if in a git repo
// 2. Home directory if not in a git repo
// 3. Filesystem root as absolute fallback
func loadProjectConfigs() Config {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}
	}

	ceiling := findCeiling(cwd)

	var configs []Config
	dir := cwd
	for {
		for _, name := range []string{"spout.yaml", ".spout.yaml"} {
			p := filepath.Join(dir, name)
			if cfg := loadFile(p); !isEmpty(cfg) {
				configs = append(configs, cfg)
				break
			}
		}
		// Stop at ceiling (git root, home, or filesystem root).
		if dir == ceiling {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Merge from outermost (parent) to innermost (deepest).
	var result Config
	for i := len(configs) - 1; i >= 0; i-- {
		result = merge(result, configs[i])
	}
	return result
}

// findCeiling returns the directory to stop walking up at.
func findCeiling(cwd string) string {
	// Try git root first.
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	// Fall back to home directory.
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func isEmpty(c Config) bool {
	return c.Name == "" && c.ServerRef == "" && len(c.Runs) == 0
}

func loadFile(path string) Config {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	yaml.Unmarshal(data, &cfg)
	return cfg
}

// loadDotenv reads a .env file and sets any unset env vars.
// Does NOT override existing env vars.
func loadDotenv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		// Don't override env vars that are already set (even if empty).
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

// WriteSystem saves the system config to disk.
func WriteSystem(c Config) error {
	dir := filepath.Dir(SystemPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(SystemPath(), data, 0644)
}

const DefaultSystemConfig = `# spout.yaml - system config (lowest priority, applies everywhere)
# Location: ~/.config/spout/spout.yaml
#
# Same schema as a project spout.yaml. Project files override this.

# Default server when no -s flag is given and no project spout.yaml sets one.
default_server: spout.sh

# Named server profiles. Use with: spout -s <name>
# Tokens go in ~/.config/spout/.env (SPOUT_TOKEN_<PROFILE>=...), not here.
# servers:
#   work:
#     url: spout.company.internal:3000
#   home:
#     url: myserver.com:3000
`
