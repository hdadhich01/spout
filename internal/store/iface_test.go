package store

import (
	"path/filepath"
	"testing"
	"time"
)

// runStoreContract exercises every Store method any backend must satisfy.
// New backends (SQLiteStore, PostgresStore) get this for free by passing
// their own factory function.
func runStoreContract(t *testing.T, factory func(t *testing.T) Store) {
	t.Helper()

	t.Run("create and get", func(t *testing.T) {
		s := factory(t)
		sess, err := s.Create(RunMeta{Name: "wolf-a3f2", Mode: "pipe", Job: "ml"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if sess.Name != "wolf-a3f2" || sess.Mode != "pipe" || sess.Job != "ml" {
			t.Fatalf("Session metadata wrong: %+v", sess)
		}
		if !sess.Active {
			t.Fatal("new session should be active")
		}

		got := s.Get("wolf-a3f2")
		if got == nil || got.Name != "wolf-a3f2" {
			t.Fatalf("Get: %+v", got)
		}
	})

	t.Run("get with prefix match", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "wolf-a3f2", Mode: "pipe"})
		s.Create(RunMeta{Name: "fox-b9c1", Mode: "pipe"})

		// Prefix "wolf" matches "wolf-a3f2" (single match → return it).
		got := s.Get("wolf")
		if got == nil || got.Name != "wolf-a3f2" {
			t.Fatalf("prefix match: %+v", got)
		}

		// Unknown prefix → nil.
		if g := s.Get("unknown"); g != nil {
			t.Fatalf("unknown prefix should return nil, got %+v", g)
		}
	})

	t.Run("get prefix is ambiguous", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "wolf-a3f2", Mode: "pipe"})
		s.Create(RunMeta{Name: "wolf-b1c2", Mode: "pipe"})

		// "wolf" matches both → ambiguous → nil.
		if got := s.Get("wolf"); got != nil {
			t.Fatalf("ambiguous prefix should return nil, got %+v", got)
		}
	})

	t.Run("resolve returns full name from prefix", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "wolf-a3f2", Mode: "pipe"})

		if got := s.Resolve("wolf-a3f2"); got != "wolf-a3f2" {
			t.Fatalf("Resolve exact: %s", got)
		}
		if got := s.Resolve("wolf"); got != "wolf-a3f2" {
			t.Fatalf("Resolve prefix: %s", got)
		}
		// Unknown name returns the input unchanged.
		if got := s.Resolve("nope"); got != "nope" {
			t.Fatalf("Resolve unknown: %s", got)
		}
	})

	t.Run("list newest first", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "first", Mode: "pipe"})
		// Sleep so the second StartedAt is strictly later.
		time.Sleep(2 * time.Millisecond)
		s.Create(RunMeta{Name: "second", Mode: "pipe"})

		list := s.List()
		if len(list) != 2 {
			t.Fatalf("List: expected 2, got %d", len(list))
		}
		if list[0].Name != "second" || list[1].Name != "first" {
			t.Fatalf("List order wrong: %s, %s", list[0].Name, list[1].Name)
		}
	})

	t.Run("rename updates identity", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "old", Mode: "pipe"})

		if err := s.Rename("old", "new"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if s.Get("old") != nil {
			t.Fatal("old name should be gone")
		}
		got := s.Get("new")
		if got == nil || got.Name != "new" {
			t.Fatalf("Get after rename: %+v", got)
		}
	})

	t.Run("rename to existing name fails", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "a", Mode: "pipe"})
		s.Create(RunMeta{Name: "b", Mode: "pipe"})

		if err := s.Rename("a", "b"); err == nil {
			t.Fatal("expected error renaming to existing name")
		}
	})

	t.Run("rename of unknown name fails", func(t *testing.T) {
		s := factory(t)
		if err := s.Rename("nope", "nada"); err == nil {
			t.Fatal("expected error renaming unknown")
		}
	})

	t.Run("delete removes session", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "doomed", Mode: "pipe"})

		if err := s.Delete("doomed"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if s.Get("doomed") != nil {
			t.Fatal("session should be gone after Delete")
		}
		if len(s.List()) != 0 {
			t.Fatalf("List after Delete: expected empty, got %d", len(s.List()))
		}
	})

	t.Run("delete unknown returns error", func(t *testing.T) {
		s := factory(t)
		if err := s.Delete("nope"); err == nil {
			t.Fatal("expected error deleting unknown")
		}
	})

	t.Run("write then history returns bytes", func(t *testing.T) {
		s := factory(t)
		sess, _ := s.Create(RunMeta{Name: "writer", Mode: "pipe"})
		sess.Write([]byte("hello "))
		sess.Write([]byte("world\n"))

		got := sess.History()
		if string(got) != "hello world\n" {
			t.Fatalf("History: %q", got)
		}
		if sess.BytesRecv != 12 {
			t.Fatalf("BytesRecv: %d", sess.BytesRecv)
		}
		if sess.Lines != 1 {
			t.Fatalf("Lines: %d", sess.Lines)
		}
	})

	t.Run("subscribe gets history snapshot and live data", func(t *testing.T) {
		s := factory(t)
		sess, _ := s.Create(RunMeta{Name: "sub", Mode: "pipe"})
		sess.Write([]byte("first\n"))

		ch, history, active := sess.Subscribe()
		if !active {
			t.Fatal("session should be active")
		}
		if string(history) != "first\n" {
			t.Fatalf("history snapshot: %q", history)
		}

		go sess.Write([]byte("second\n"))

		select {
		case data := <-ch:
			if string(data) != "second\n" {
				t.Fatalf("live data: %q", data)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for live data")
		}
		sess.Unsubscribe(ch)
	})

	t.Run("close marks inactive and closes viewers", func(t *testing.T) {
		s := factory(t)
		sess, _ := s.Create(RunMeta{Name: "close", Mode: "pipe"})

		ch, _, _ := sess.Subscribe()
		sess.Close()

		if sess.Active {
			t.Fatal("session should be inactive after Close")
		}
		// Channel should be closed.
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("viewer channel should be closed after session Close")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for channel close")
		}
	})

	t.Run("status taxonomy", func(t *testing.T) {
		s := factory(t)

		// Pipe mode: streaming while active, success on close.
		pipe, _ := s.Create(RunMeta{Name: "pipe", Mode: "pipe"})
		pipe.Write([]byte("data"))
		if got := pipe.Status(); got != "streaming" {
			t.Fatalf("pipe streaming: %s", got)
		}
		pipe.Close()
		if got := pipe.Status(); got != "success" {
			t.Fatalf("pipe success: %s", got)
		}

		// Run mode: killed when no exit marker.
		run, _ := s.Create(RunMeta{Name: "run-kill", Mode: "run"})
		run.Close()
		if got := run.Status(); got != "killed" {
			t.Fatalf("run killed: %s", got)
		}

		// Run mode: success with exit code 0.
		ok, _ := s.Create(RunMeta{Name: "run-ok", Mode: "run"})
		ok.Write([]byte("\x1b]9999;0\x07"))
		ok.Close()
		if got := ok.Status(); got != "success" {
			t.Fatalf("run success: %s", got)
		}

		// Run mode: error with non-zero exit code.
		err, _ := s.Create(RunMeta{Name: "run-err", Mode: "run"})
		err.Write([]byte("\x1b]9999;42\x07"))
		err.Close()
		if got := err.Status(); got != "error" {
			t.Fatalf("run error: %s", got)
		}
		if err.ExitCode != 42 {
			t.Fatalf("ExitCode: %d", err.ExitCode)
		}
	})

	t.Run("savemeta is idempotent", func(t *testing.T) {
		s := factory(t)
		sess, _ := s.Create(RunMeta{Name: "save", Mode: "pipe"})
		sess.Write([]byte("data"))
		s.SaveMeta(sess)
		s.SaveMeta(sess) // again — should not error
	})

	t.Run("append and read events", func(t *testing.T) {
		s := factory(t)
		s.Create(RunMeta{Name: "evented", Mode: "pipe"})

		// No events yet → nil.
		if got := s.Events("evented"); got != nil {
			t.Fatalf("expected nil events, got %q", got)
		}

		// Append two events; a trailing newline on input is normalized.
		if err := s.AppendEvent("evented", []byte(`{"id":"e1"}`)); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		if err := s.AppendEvent("evented", []byte(`{"id":"e2"}`+"\n")); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		want := `{"id":"e1"}` + "\n" + `{"id":"e2"}` + "\n"
		if got := string(s.Events("evented")); got != want {
			t.Fatalf("Events = %q, want %q", got, want)
		}
	})

	t.Run("append event for fresh run creates folder", func(t *testing.T) {
		s := factory(t)
		// No Create first — events can race ahead of metadata.
		if err := s.AppendEvent("ghost", []byte(`{"id":"x"}`)); err != nil {
			t.Fatalf("AppendEvent on fresh run: %v", err)
		}
		if got := string(s.Events("ghost")); got != `{"id":"x"}`+"\n" {
			t.Fatalf("Events = %q", got)
		}
	})
}

// TestFileStore runs the contract suite against the file-backed implementation.
func TestFileStore(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		dir := filepath.Join(t.TempDir(), "store")
		fs, err := New(dir)
		if err != nil {
			t.Fatalf("FileStore.New: %v", err)
		}
		return fs
	})
}

// TestFileStoreReload checks that a FileStore can recover its state from disk.
func TestFileStoreReload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	{
		s, err := New(dir)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		sess, _ := s.Create(RunMeta{Name: "persisted", Mode: "pipe", Job: "ml"})
		sess.Write([]byte("hello\n"))
		s.SaveMeta(sess)
		sess.Close()
		s.SaveMeta(sess)
	}

	// Re-open the same dir.
	s2, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := s2.Get("persisted")
	if got == nil {
		t.Fatal("session not loaded from disk")
	}
	if got.Name != "persisted" || got.Job != "ml" {
		t.Fatalf("loaded metadata wrong: %+v", got)
	}
	if string(got.History()) != "hello\n" {
		t.Fatalf("loaded history: %q", got.History())
	}
	// Should be marked inactive on reload.
	if got.Active {
		t.Fatal("loaded session should be inactive")
	}
}
