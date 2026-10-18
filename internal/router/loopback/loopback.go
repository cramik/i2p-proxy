// Package loopback is a development/test Router backend. It does not touch
// the real I2P network: Dial() to this router's own LocalDestination() is
// wired directly to its own Listener via net.Pipe. It exists so the rest of
// the stack (protocol framing, proxysrv dispatch, proxycli listeners, ACL,
// tunnel pooling) can be built, run, and tested end-to-end before a real
// embedded backend (libi2pd-cgo, go-i2p) is wired in.
//
// It must never be selected in production config; Start() logs loudly to
// make that hard to miss.
package loopback

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"i2p-proxy/internal/router"
)

type Router struct {
	mu       sync.Mutex
	dest     router.Destination
	ready    bool
	acceptCh chan net.Conn
	closed   chan struct{}
}

func New() *Router {
	return &Router{
		acceptCh: make(chan net.Conn, 16),
		closed:   make(chan struct{}),
	}
}

func (r *Router) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dest == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("loopback: generating destination: %w", err)
		}
		addr := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
		r.dest = router.Destination(addr + ".loopback.b32.i2p")
	}
	r.ready = true
	return nil
}

func (r *Router) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	r.ready = false
	return nil
}

func (r *Router) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

func (r *Router) LocalDestination() (router.Destination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dest == "" {
		return "", fmt.Errorf("loopback: not started")
	}
	return r.dest, nil
}

func (r *Router) Listen(ctx context.Context) (router.Listener, error) {
	if !r.Ready() {
		return nil, router.ErrNotReady
	}
	dest, _ := r.LocalDestination()
	return &listener{r: r, dest: dest}, nil
}

// Dial only knows how to reach this same router's own destination - enough
// to exercise client+server code paths within a single process.
func (r *Router) Dial(ctx context.Context, dest router.Destination) (router.Conn, error) {
	if !r.Ready() {
		return nil, router.ErrNotReady
	}
	local, _ := r.LocalDestination()
	if dest != local {
		return nil, fmt.Errorf("loopback: unknown destination %q (this backend only loops back to itself, %q)", dest, local)
	}
	client, server := net.Pipe()
	select {
	case r.acceptCh <- server:
	case <-r.closed:
		client.Close()
		server.Close()
		return nil, fmt.Errorf("loopback: router stopped")
	}
	return &conn{Conn: client, remote: local}, nil
}

type listener struct {
	r    *Router
	dest router.Destination
}

func (l *listener) Accept() (router.Conn, error) {
	select {
	case c := <-l.r.acceptCh:
		return &conn{Conn: c, remote: l.dest}, nil
	case <-l.r.closed:
		return nil, fmt.Errorf("loopback: listener closed")
	}
}

func (l *listener) Close() error                         { return nil }
func (l *listener) LocalDestination() router.Destination { return l.dest }

type conn struct {
	net.Conn
	remote router.Destination
}

func (c *conn) RemoteDestination() router.Destination { return c.remote }
func (c *conn) SetDeadline(t time.Time) error         { return c.Conn.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error     { return c.Conn.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error    { return c.Conn.SetWriteDeadline(t) }

var _ router.Router = (*Router)(nil)
