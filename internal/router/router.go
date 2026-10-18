// Package router defines the contract every embedded I2P backend must satisfy.
//
// "Embedded" is a hard constraint: an implementation must run the I2P router
// entirely in-process (pure Go, or a statically-linked C/C++ library driven
// via cgo). No implementation may shell out to, or otherwise depend on, an
// external i2p/i2pd daemon (no exec.Command, no SAM bridge to a separately
// running process, no reliance on a system-installed router).
package router

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotReady is returned when an operation is attempted before the router
// has built enough tunnels to be usable.
var ErrNotReady = errors.New("router: not ready")

// Destination is an I2P destination address, e.g. a base32
// "abcdef...ghij.b32.i2p" string, or a full base64 destination.
type Destination string

// Conn is a bidirectional I2P streaming connection, analogous to net.Conn
// but keyed by I2P destinations instead of IP:port.
type Conn interface {
	io.ReadWriteCloser
	RemoteDestination() Destination
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// Listener accepts inbound I2P streaming connections addressed to this
// router's local Destination - the eepsite side of the service.
type Listener interface {
	Accept() (Conn, error)
	Close() error
	LocalDestination() Destination
}

// Router is the embedded I2P router backend contract.
type Router interface {
	// Start brings the embedded router up: loads/generates keys, connects to
	// the I2P network (NTCP2/SSU2 transports), builds initial tunnels. It
	// blocks until the router is at least minimally operational or ctx is
	// canceled.
	Start(ctx context.Context) error

	// Stop drains in-flight streams and persists router state (NetDB, keys)
	// before returning.
	Stop(ctx context.Context) error

	// Ready reports whether the router currently has enough tunnels built
	// to reliably Dial/Accept.
	Ready() bool

	// LocalDestination returns this router's own published Destination.
	// Returns an error if called before the destination keypair exists.
	LocalDestination() (Destination, error)

	// Listen opens a Listener bound to the local Destination.
	Listen(ctx context.Context) (Listener, error)

	// Dial opens an outbound I2P streaming connection to a remote Destination.
	Dial(ctx context.Context, dest Destination) (Conn, error)
}

// Config carries backend-agnostic tuning knobs. Backends may ignore fields
// they don't support.
type Config struct {
	DataDir          string
	InboundLength    int
	OutboundLength   int
	InboundQuantity  int
	OutboundQuantity int
	// Port, if nonzero, pins the NTCP2/SSU2 listen port instead of letting
	// the backend pick one automatically. Operators behind NAT that doesn't
	// preserve a stable external UDP mapping (symmetric NAT - some cloud/
	// container network stacks, some home routers) need this paired with an
	// explicit port-forward/publish rule for SSU2 (UDP) to work reliably;
	// without a stable external port, LeaseSet-publish confirmations and
	// inbound reachability can fail even though outbound connectivity works
	// fine.
	Port int

	// LogLevel sets the backend's own internal log verbosity (e.g. i2pd's
	// "debug"/"info"/"warn"/"error"/"none"), independent of this program's
	// own slog level. Empty means the backend's own default.
	LogLevel string
}
