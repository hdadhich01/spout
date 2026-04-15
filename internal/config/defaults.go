package config

// Default values used when no config exists. Every CLI/server reference to a
// hostname or port should come from this file - never hardcoded.
const (
	// DefaultRemoteHost is the public hosted spout server.
	DefaultRemoteHost = "spout.sh"

	// DefaultLocalHost is the loopback host used by `spout server` and `-l`.
	DefaultLocalHost = "localhost"

	// DefaultLocalPort is the listening port for the local server.
	DefaultLocalPort = "3000"
)

// DefaultLocalAddr returns "localhost:3000" - the address used by `-l` and the
// fallback for `findRunServer`.
func DefaultLocalAddr() string {
	return DefaultLocalHost + ":" + DefaultLocalPort
}
