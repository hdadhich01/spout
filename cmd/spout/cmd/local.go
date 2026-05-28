package cmd

import (
	"fmt"
	"strings"

	"github.com/hdadhich01/spout/internal/config"
	"github.com/hdadhich01/spout/internal/store"
)

// openLocal opens the CLI's local-copy FileStore (`~/.spout/local/` by
// default; cfg.Local overrides). Returns the store + the resolved root
// path so callers can render it in user output.
func openLocal() (*store.FileStore, string, error) {
	cfg := config.Load()
	dir := cfg.ResolveLocal()
	st, err := store.New(dir)
	if err != nil {
		return nil, dir, fmt.Errorf("opening local copy at %s: %w", dir, err)
	}
	return st, dir, nil
}

// resolveLocalTargets maps the user's name pieces onto sessions in the
// local copy. Same address grammar as the server-side resolveTargets:
//
//	resolveLocalTargets(["fox"])             -> exact name (one session)
//	resolveLocalTargets(["exp-2"])           -> whole run (every stream in it)
//	resolveLocalTargets(["training"])        -> by stream label (chooser if >1)
//	resolveLocalTargets(["exp-2","training"]) -> exact stream
//	resolveLocalTargets(["exp-2/training"])   -> same, slash form
//
// `allowChooser` controls whether an ambiguous label triggers the
// interactive picker. When false (e.g., -y), ambiguity is an error.
func resolveLocalTargets(st *store.FileStore, pieces []string, allowChooser bool) ([]*store.Session, error) {
	run, label := splitPieces(pieces)
	list := st.List()

	// Case A: explicit run + label.
	if run != "" && label != "" {
		want := run + "-" + label
		for _, s := range list {
			if s.Name == want {
				return []*store.Session{s}, nil
			}
		}
		return nil, fmt.Errorf("no stream %s in run %s", cBold(label), cBold(run))
	}

	needle := run
	if needle == "" {
		needle = label
	}

	// Exact name match always wins (covers `fox-a3f2` and `exp-2-training`).
	for _, s := range list {
		if s.Name == needle {
			return []*store.Session{s}, nil
		}
	}

	// Run-group match: every session whose Run == needle.
	var groupHits []*store.Session
	for _, s := range list {
		if s.Run != "" && s.Run == needle {
			groupHits = append(groupHits, s)
		}
	}
	if len(groupHits) > 0 {
		return groupHits, nil
	}

	// Stream-label match across runs. May be 1 or many.
	var labelHits []*store.Session
	for _, s := range list {
		if s.Label != "" && s.Label == needle {
			labelHits = append(labelHits, s)
		}
	}
	if len(labelHits) == 1 {
		return labelHits, nil
	}
	if len(labelHits) > 1 {
		if !allowChooser {
			return nil, fmt.Errorf("%d streams named %s — pick one with %s",
				len(labelHits), cBold(needle), cAqua("<run>/"+needle))
		}
		picked, err := chooseAmbiguousLocal(labelHits)
		if err != nil {
			return nil, err
		}
		return []*store.Session{picked}, nil
	}

	// Last resort: prefix match (legacy behavior).
	if s := st.Get(needle); s != nil {
		return []*store.Session{s}, nil
	}

	return nil, fmt.Errorf("no run or stream named %s", cBold(needle))
}

// resolveLocalSingle is the convenience wrapper for commands that want
// exactly one target (rename/share/logs). Errors if pieces resolve to a
// whole run group.
func resolveLocalSingle(st *store.FileStore, pieces []string) (*store.Session, error) {
	hits, err := resolveLocalTargets(st, pieces, true)
	if err != nil {
		return nil, err
	}
	if len(hits) > 1 {
		return nil, fmt.Errorf("%s is a multi-stream run — pick one stream with %s",
			cBold(hits[0].Run), cAqua("<run>/<stream>"))
	}
	return hits[0], nil
}

// findLocalRun is a thin compatibility wrapper for callers that still
// pass a single bare name string. Prefer resolveLocalSingle for new code.
func findLocalRun(st *store.FileStore, name string) (*store.Session, error) {
	return resolveLocalSingle(st, []string{name})
}

// chooseAmbiguousLocal prompts when one stream label matches multiple
// runs in the local copy.
func chooseAmbiguousLocal(candidates []*store.Session) (*store.Session, error) {
	fmt.Fprintln(stderr)
	info("%d streams named %s — pick one:", len(candidates), cBold(candidates[0].Label))
	fmt.Fprintln(stderr)
	for i, s := range candidates {
		fmt.Fprintf(stderr, "  %s %s\n",
			cAqua(cBold(fmt.Sprintf("[%d]", i+1))),
			displayLocalTarget(s))
	}
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")
	raw := strings.TrimSpace(askLine(""))
	if raw == "" {
		return nil, fmt.Errorf("cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err != nil || idx < 1 || idx > len(candidates) {
		return nil, fmt.Errorf("invalid choice %q", raw)
	}
	return candidates[idx-1], nil
}

// displayLocalTarget renders a session for chooser/error output. Includes
// the job prefix when set, so the same stream label across different
// jobs is visually disambiguated.
//
//	[ml-training] exp-1/training
//	exp-1/training            (no job)
//	fox-a3f2                  (standalone)
func displayLocalTarget(s *store.Session) string {
	if s.Run != "" && s.Label != "" {
		base := cBold(s.Run) + cDim("/") + cBold(s.Label)
		if s.Job != "" {
			return cDim("["+s.Job+"]") + " " + base
		}
		return base
	}
	if s.Job != "" {
		return cDim("["+s.Job+"]") + " " + cBold(s.Name)
	}
	return cBold(s.Name)
}

// pickLocalRun shows a numbered list of runs (grouped by Run when
// applicable) from the local copy and returns the chosen identifier
// (run name for groups, full session name for standalones). Empty
// input cancels.
func pickLocalRun(st *store.FileStore, promptText string) (string, error) {
	list := st.List()
	if len(list) == 0 {
		return "", fmt.Errorf("no local runs to %s", promptText)
	}

	// Group sessions by Run name; standalones become their own group.
	type group struct {
		key      string
		streams  []*store.Session
		standalone bool
	}
	seen := map[string]int{}
	var groups []group
	for _, s := range list {
		k := s.Run
		standalone := false
		if k == "" {
			k = s.Name
			standalone = true
		}
		if i, ok := seen[k]; ok {
			groups[i].streams = append(groups[i].streams, s)
			continue
		}
		seen[k] = len(groups)
		groups = append(groups, group{key: k, streams: []*store.Session{s}, standalone: standalone})
	}

	fmt.Fprintln(stderr)
	info("pick a run to %s:", cBold(promptText))
	fmt.Fprintln(stderr)
	for i, g := range groups {
		dot := statusDot(g.streams[0].Status())
		jobPrefix := ""
		if g.streams[0].Job != "" {
			jobPrefix = cDim("["+g.streams[0].Job+"] ")
		}
		extra := ""
		if !g.standalone && len(g.streams) > 0 {
			unit := "streams"
			if len(g.streams) == 1 {
				unit = "stream"
			}
			extra = cDim(fmt.Sprintf(" (%d %s)", len(g.streams), unit))
		}
		fmt.Fprintf(stderr, "  %s %s  %s%s%s\n",
			cAqua(cBold(fmt.Sprintf("[%d]", i+1))),
			dot,
			jobPrefix,
			cBold(g.key),
			extra)
	}
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, "  > ")
	raw := strings.TrimSpace(askLine(""))
	if raw == "" {
		return "", fmt.Errorf("cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(raw, "%d", &idx); err != nil || idx < 1 || idx > len(groups) {
		return "", fmt.Errorf("invalid choice %q", raw)
	}
	return groups[idx-1].key, nil
}
