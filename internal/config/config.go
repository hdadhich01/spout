package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is the merged result of every spout.yaml found during walk-up.
// One schema - global and project files map to the same struct.
// See docs/config.md for the authoritative field reference.
type Config struct {
	// Routing (scalar, override).
	DefaultServer string `yaml:"default_server,omitempty"`
	ServerRef     string `yaml:"server,omitempty"` // profile name or host:port
	Job           string `yaml:"job,omitempty"`    // dashboard grouping label
	RunName       string `yaml:"run_name,omitempty"`

	// Storage (scalar, override; typically global-only).
	// `local:` is the CLI's canonical local copy (`~/.spout/local/` by
	// default). `storage:` is the server's data dir (`~/.spout/server/`
	// by default). They MUST be different paths even when colocated —
	// see ARCHITECTURE.md §2 (local copy is canonical).
	Local   string `yaml:"local,omitempty"`
	Storage string `yaml:"storage,omitempty"`
	History *bool  `yaml:"history,omitempty"` // pointer so "unset" != "false"

	// Address book (map, merge).
	Servers map[string]Server `yaml:"servers,omitempty"`

	// Execution blueprint (list, replace).
	Streams []Stream `yaml:"streams,omitempty"`

	// Observability (object, merged field-by-field; Rules list replaces).
	Observe *Observe `yaml:"observe,omitempty"`

	// Deprecated: renamed to `observe`. Parsed only so a leftover `watch:`
	// block migrates forward (with a one-time warning) instead of silently
	// vanishing. Promoted into Observe by Load; never used directly.
	Watch *Observe `yaml:"watch,omitempty"`

	// sourceFile tracks which yaml file this Config was parsed from, so
	// stream-relative `dir:` values can later be resolved to absolute paths.
	// Not serialized.
	sourceFile string `yaml:"-"`
}

// Server is a named server profile. `URL` is the address; `Token` names the
// env var that holds the auth token for this server (the value itself lives
// in .env so it never gets committed alongside the yaml).
//
// Tokens are explicit-only: if a profile does NOT declare `token:`, no
// token is sent for that server. There is no implicit fallback to
// SPOUT_TOKEN_<PROFILE> or SPOUT_TOKEN - you opt in by naming the var.
type Server struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token,omitempty"` // name of the env var, not the value
}

// Stream is one tmux pane in a bare-`spout` launch.
type Stream struct {
	Label   string            `yaml:"label"`
	Command string            `yaml:"command"`
	Dir     string            `yaml:"dir,omitempty"` // relative to owning yaml file
	Env     map[string]string `yaml:"env,omitempty"`

	// SourceFile records the yaml file this stream was defined in, so the
	// CLI can resolve `Dir` relative to the correct directory. Not serialized.
	SourceFile string `yaml:"-"`
}

// Observe is the LLM-powered observability subsystem: a CLI-side loop that
// periodically feeds recent output to a model and emits structured events
// (status, metrics, synthesis, and built-in agent-failure detectors). One
// engine, many consumers — see internal/observe and ARCHITECTURE.md.
type Observe struct {
	Enabled         bool              `yaml:"enabled,omitempty"`
	DefaultInterval string            `yaml:"default_interval,omitempty"` // fallback poll cadence
	Model           *ObserveModel     `yaml:"model,omitempty"`
	Detectors       *ObserveDetectors `yaml:"detectors,omitempty"`
	Rules           []ObserveRule     `yaml:"rules,omitempty"`
	OTel            string            `yaml:"otel,omitempty"` // OTLP/HTTP endpoint to also export events to
}

// ObserveModel identifies the backing LLM for the observer.
type ObserveModel struct {
	Type     string `yaml:"type,omitempty"`     // "api" | "local" | "cli" (default api)
	Endpoint string `yaml:"endpoint,omitempty"` // required when type=local
	Model    string `yaml:"model,omitempty"`    // model id (default DefaultObserveModelID)
	Token    string `yaml:"token,omitempty"`    // env-var NAME holding the API key, not the value
}

// ObserveDetectors toggles the built-in, zero-config detectors. Pointers so
// an unset field inherits the default (on while observe is enabled) rather
// than reading as an explicit false.
type ObserveDetectors struct {
	Classify *bool `yaml:"classify,omitempty"` // auto-detect run type
	Loop     *bool `yaml:"loop,omitempty"`     // repeated identical failure/attempt
	Drift    *bool `yaml:"drift,omitempty"`    // agent diverging from stated intent
	Amnesia  *bool `yaml:"amnesia,omitempty"`  // re-asking / re-doing / constraint drop
}

// ObserveRule is one user-defined prompt folded into the observer's single
// LLM call. `sources` (optional) must match Stream labels when set.
type ObserveRule struct {
	Name     string   `yaml:"name"`
	Prompt   string   `yaml:"prompt"`
	Interval string   `yaml:"interval,omitempty"`
	Sources  []string `yaml:"sources,omitempty"`
}

// ModelType returns the resolved backing-LLM kind, defaulting to api.
func (o *Observe) ModelType() string {
	if o == nil || o.Model == nil || o.Model.Type == "" {
		return DefaultObserveModelType
	}
	return o.Model.Type
}

// ModelID returns the resolved model identifier (only meaningful for api).
func (o *Observe) ModelID() string {
	if o == nil || o.Model == nil || o.Model.Model == "" {
		return DefaultObserveModelID
	}
	return o.Model.Model
}

// Endpoint returns the resolved local-model endpoint.
func (o *Observe) Endpoint() string {
	if o == nil || o.Model == nil || o.Model.Endpoint == "" {
		return DefaultObserveEndpoint
	}
	return o.Model.Endpoint
}

// APIKey resolves the provider API key from the named env var, falling back
// to the Anthropic SDK's own default var so dev needs no extra config.
func (o *Observe) APIKey() string {
	v := DefaultObserveTokenVar
	if o != nil && o.Model != nil && o.Model.Token != "" {
		v = o.Model.Token
	}
	return os.Getenv(v)
}

// Detector reports whether a named built-in detector is active. Unset
// detectors default on while observe is enabled.
func (o *Observe) Detector(name string) bool {
	if o == nil || !o.Enabled {
		return false
	}
	d := o.Detectors
	on := func(p *bool) bool { return p == nil || *p }
	if d == nil {
		return true
	}
	switch name {
	case "classify":
		return on(d.Classify)
	case "loop":
		return on(d.Loop)
	case "drift":
		return on(d.Drift)
	case "amnesia":
		return on(d.Amnesia)
	}
	return false
}

// Load reads every spout.yaml on the walk-up path plus the global file, then
// merges them (deepest wins) and loads .env files. Does not validate; call
// Validate() to check foreign-key constraints.
func Load() Config {
	migrateLegacy()
	sys := loadFile(SystemPath())
	proj := loadProjectConfigs()
	cfg := merge(sys, proj)
	loadEnvTokens(&cfg)
	cfg.resolveLegacyWatch()
	return cfg
}

var warnLegacyWatch sync.Once

// resolveLegacyWatch promotes a deprecated `watch:` block into `observe:`
// (observe wins if both are present) and warns once per process. The
// watchdog never executed under its old name, so this is a clean rename
// with a courtesy nudge rather than a behavioral migration.
func (c *Config) resolveLegacyWatch() {
	if c.Watch == nil {
		return
	}
	warnLegacyWatch.Do(func() {
		fmt.Fprintln(os.Stderr, "spout: `watch:` in spout.yaml is deprecated — rename it to `observe:`")
	})
	if c.Observe == nil {
		c.Observe = c.Watch
	}
	c.Watch = nil
}

// Validate checks invariants that can't be expressed in the struct alone:
// foreign-key integrity between observe.rules and streams, and that a
// local model names its endpoint. Returns nil when there's nothing to check.
func (c Config) Validate() error {
	o := c.Observe
	if o == nil {
		return nil
	}
	labels := make(map[string]struct{}, len(c.Streams))
	for _, s := range c.Streams {
		labels[s.Label] = struct{}{}
	}
	for _, r := range o.Rules {
		for _, src := range r.Sources {
			if _, ok := labels[src]; !ok {
				return fmt.Errorf("observe rule %q references unknown stream %q (define it under streams:)", r.Name, src)
			}
		}
	}
	if o.Model != nil && o.Model.Type == "local" && o.Model.Endpoint == "" {
		return fmt.Errorf("observe.model.type=local requires observe.model.endpoint")
	}
	return nil
}

// LoadedFiles returns absolute paths of every spout.yaml and .env that get
// merged for the current cwd. Deepest-first order. Used by `spout config`.
func LoadedFiles() (configs []string, envs []string) {
	migrateLegacy()

	cwd, err := os.Getwd()
	if err == nil {
		ceiling := findCeiling(cwd)

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

	if _, err := os.Stat(SystemPath()); err == nil {
		configs = append(configs, SystemPath())
	}
	sysEnv := filepath.Join(filepath.Dir(SystemPath()), ".env")
	if _, err := os.Stat(sysEnv); err == nil {
		envs = append(envs, sysEnv)
	}
	return
}

// Resolve returns the server URL and token for a given CLI flag input.
// Priority: CLI flag > merged server ref > merged default_server > remote default.
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

	if c.Servers != nil {
		if srv, ok := c.Servers[input]; ok {
			return srv.URL, resolveTokenForServer(srv)
		}
	}
	// Raw host:port - no profile, no token. Use -s to select a profile if
	// you need auth.
	return input, ""
}

// resolveTokenForServer returns the auth token for a server profile.
// Explicit-only: if the profile doesn't name an env var via `token:`,
// there is no token.
func resolveTokenForServer(srv Server) string {
	if srv.Token == "" {
		return ""
	}
	return os.Getenv(srv.Token)
}

// ResolvedProfile returns the profile NAME selected for cliFlag using the
// same priority ladder as Resolve (cliFlag > ServerRef > DefaultServer),
// or "" if the resolved input is a raw host:port not in the address book.
// Used to look up the env-var name for the auth token without exposing
// the secret value (which Resolve already returns).
func (c Config) ResolvedProfile(cliFlag string) string {
	input := cliFlag
	if input == "" {
		input = c.ServerRef
	}
	if input == "" {
		input = c.DefaultServer
	}
	if input == "" {
		return ""
	}
	if _, ok := c.Servers[input]; ok {
		return input
	}
	return ""
}

// ResolveLocal returns the CLI's local-copy directory: cfg.Local when
// set (with leading ~ expanded to $HOME), otherwise DefaultLocalDir().
// The CLI's local copy is intentionally separate from the server's
// storage dir — see ARCHITECTURE.md §2.
func (c Config) ResolveLocal() string {
	if c.Local == "" {
		return DefaultLocalDir()
	}
	p := c.Local
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// TokenVarFor returns the env-var name a profile reads its token from,
// or empty if the profile doesn't declare one. Used by `spout config`
// to render the token row honestly instead of guessing at conventions.
func (c Config) TokenVarFor(profile string) string {
	if profile == "" || c.Servers == nil {
		return ""
	}
	srv, ok := c.Servers[profile]
	if !ok {
		return ""
	}
	return srv.Token
}

// loadEnvTokens loads system .env, then walks up from cwd loading each .env
// (outermost first, so the deepest file wins per variable).
func loadEnvTokens(cfg *Config) {
	loadDotenv(filepath.Join(filepath.Dir(SystemPath()), ".env"))

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
	for i := len(envPaths) - 1; i >= 0; i-- {
		loadDotenv(envPaths[i])
	}
}

// Exists returns true if the system spout.yaml exists on disk.
func Exists() bool {
	_, err := os.Stat(SystemPath())
	return err == nil
}

// Context is the discovery context for the current cwd.
type Context struct {
	Cwd      string
	Ceiling  string // git root or home dir
	IsGitDir bool
}

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
func SystemPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout", "spout.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout", "spout.yaml")
}

func legacySystemPath() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "spout", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spout", "config.yaml")
}

// migrateLegacy renames a leftover ~/.config/spout/config.yaml → spout.yaml.
func migrateLegacy() {
	old := legacySystemPath()
	new := SystemPath()
	if old == new {
		return
	}
	if _, err := os.Stat(new); err == nil {
		return
	}
	if _, err := os.Stat(old); err == nil {
		os.Rename(old, new)
	}
}

// merge folds a project config onto a system/base config.
// Merge semantics per docs/config.md:
//
//	Map     -> MERGE, deepest per key      (servers, observe sub-scalars)
//	List    -> REPLACE, deepest wins       (streams, observe.rules)
//	Scalar  -> OVERRIDE, deepest wins      (server, job, run_name, ...)
//
// `proj` is closer to cwd than `sys`, so proj wins.
func merge(sys, proj Config) Config {
	out := sys

	// Scalars - override if proj set them.
	if proj.DefaultServer != "" {
		out.DefaultServer = proj.DefaultServer
	}
	if proj.ServerRef != "" {
		out.ServerRef = proj.ServerRef
	}
	if proj.Job != "" {
		out.Job = proj.Job
	}
	if proj.RunName != "" {
		out.RunName = proj.RunName
	}
	if proj.Local != "" {
		out.Local = proj.Local
	}
	if proj.Storage != "" {
		out.Storage = proj.Storage
	}
	if proj.History != nil {
		out.History = proj.History
	}

	// Maps - merge, proj wins per key.
	if len(proj.Servers) > 0 {
		if out.Servers == nil {
			out.Servers = make(map[string]Server)
		}
		for k, v := range proj.Servers {
			out.Servers[k] = v
		}
	}

	// Lists - proj replaces entirely when set.
	if len(proj.Streams) > 0 {
		out.Streams = proj.Streams
	}

	// Observe - object merge. Sub-scalars override; Rules list replaces.
	// The deprecated `watch:` alias merges the same way (promoted in Load).
	out.Observe = mergeObserve(out.Observe, proj.Observe)
	out.Watch = mergeObserve(out.Watch, proj.Watch)

	return out
}

// mergeObserve folds a project Observe block onto a base one: scalars and
// model/detector sub-fields override when set, Rules replace when non-empty.
func mergeObserve(out, proj *Observe) *Observe {
	if proj == nil {
		return out
	}
	if out == nil {
		out = &Observe{}
	}
	if proj.Enabled {
		out.Enabled = true
	}
	if proj.DefaultInterval != "" {
		out.DefaultInterval = proj.DefaultInterval
	}
	if proj.OTel != "" {
		out.OTel = proj.OTel
	}
	if proj.Model != nil {
		if out.Model == nil {
			out.Model = &ObserveModel{}
		}
		if proj.Model.Type != "" {
			out.Model.Type = proj.Model.Type
		}
		if proj.Model.Endpoint != "" {
			out.Model.Endpoint = proj.Model.Endpoint
		}
		if proj.Model.Model != "" {
			out.Model.Model = proj.Model.Model
		}
		if proj.Model.Token != "" {
			out.Model.Token = proj.Model.Token
		}
	}
	if proj.Detectors != nil {
		if out.Detectors == nil {
			out.Detectors = &ObserveDetectors{}
		}
		if proj.Detectors.Classify != nil {
			out.Detectors.Classify = proj.Detectors.Classify
		}
		if proj.Detectors.Loop != nil {
			out.Detectors.Loop = proj.Detectors.Loop
		}
		if proj.Detectors.Drift != nil {
			out.Detectors.Drift = proj.Detectors.Drift
		}
		if proj.Detectors.Amnesia != nil {
			out.Detectors.Amnesia = proj.Detectors.Amnesia
		}
	}
	if len(proj.Rules) > 0 {
		out.Rules = proj.Rules
	}
	return out
}

// loadProjectConfigs walks up cwd → ceiling collecting spout.yaml files,
// merges from outermost to innermost so the deepest wins.
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
		if dir == ceiling {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	var result Config
	for i := len(configs) - 1; i >= 0; i-- {
		result = merge(result, configs[i])
	}
	return result
}

func findCeiling(cwd string) string {
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

// isEmpty returns true if a parsed Config carries no interesting fields.
// Used to skip empty/missing files during walk-up without polluting the
// merge pipeline.
func isEmpty(c Config) bool {
	return c.DefaultServer == "" &&
		c.ServerRef == "" &&
		c.Job == "" &&
		c.RunName == "" &&
		c.Storage == "" &&
		c.Local == "" &&
		c.History == nil &&
		len(c.Servers) == 0 &&
		len(c.Streams) == 0 &&
		c.Observe == nil &&
		c.Watch == nil
}

// loadFile parses a single yaml file. Records the sourceFile on the struct
// plus on every stream so relative paths can be resolved later.
func loadFile(path string) Config {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	yaml.Unmarshal(data, &cfg)
	cfg.sourceFile = path
	for i := range cfg.Streams {
		cfg.Streams[i].SourceFile = path
	}
	return cfg
}

// loadDotenv reads a .env file and sets any currently-unset env vars.
// Does NOT override vars already present in the process environment.
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

// EnsureSystemConfig writes the global template to ~/.config/spout/spout.yaml
// if that file is missing or empty. Returns true if a write happened.
// Called on CLI startup so fresh installs get an annotated starter file
// without requiring `spout login`.
func EnsureSystemConfig() (wrote bool, err error) {
	p := SystemPath()
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		return false, nil
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	}
	return true, os.WriteFile(p, []byte(GlobalTemplate), 0644)
}
