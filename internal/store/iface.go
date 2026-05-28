// Package store defines the persistence layer for Spout runs.
//
// A `Store` manages a collection of `Session`s. Each Session represents one
// run — its metadata + its byte stream + its viewers. The Store interface
// is backend-agnostic; today only FileStore is implemented (folder-per-run
// on local disk). Future backends (SQLiteStore, PostgresStore + R2) will
// satisfy the same interface — see ARCHITECTURE.md §6.
//
// Design intent:
//   - Session is a shared struct (data + in-memory viewer fan-out logic).
//     Different backends differ in PERSISTENCE only; the Session shape
//     itself is universal.
//   - Store is the abstraction over persistence: how runs are created,
//     listed, renamed, deleted. Backends implement it.
//   - Server code depends on the Store interface (not a concrete impl)
//     so swapping backends is a one-line change at startup.
package store

// Store is the persistence abstraction for Spout runs.
//
// Implementations:
//   - FileStore (file.go): folder-per-run on local disk. Default.
//   - SQLiteStore [planned]: metadata in SQLite, bytes still files.
//   - PostgresStore + R2 [planned]: hosted spout.sh.
//
// The contract test suite in iface_test.go exercises every method any
// implementation must satisfy.
type Store interface {
	// Create allocates a new active session with the given metadata.
	// The returned Session is ready to Write into.
	// If a session with the same name already exists, it is closed first.
	Create(m RunMeta) (*Session, error)

	// Get returns a session by name, or nil if not found.
	// Supports prefix matching: if no exact match, returns the only
	// session whose name starts with `<name>-`. Ambiguous prefix
	// matches return nil.
	Get(name string) *Session

	// Resolve returns the full session name for a given prefix,
	// or the input unchanged if no match.
	Resolve(name string) string

	// List returns all sessions, newest StartedAt first.
	List() []*Session

	// Rename changes a session's identity. Returns an error if the
	// source doesn't exist or the destination already does.
	Rename(oldName, newName string) error

	// Delete removes a session and its persisted state.
	Delete(name string) error

	// SaveMeta persists a session's metadata to the backend.
	// Called after lifecycle events (Close, Rename) and on demand.
	SaveMeta(sess *Session)

	// AppendEvent appends one observability event (raw JSON, one line) to a
	// run's event log. The store treats the bytes as opaque — it never parses
	// them — keeping the server a thin fan-out hub (ARCHITECTURE.md §11).
	AppendEvent(name string, raw []byte) error

	// Events returns a run's raw event log (newline-delimited JSON), or nil
	// when there are none.
	Events(name string) []byte
}
