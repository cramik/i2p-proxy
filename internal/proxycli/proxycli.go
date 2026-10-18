// Package proxycli is the client side: it runs the embedded router locally
// and exposes plain local listeners that tunnel to a remote server's
// Destination over I2P - either a fixed port-forward to one of the
// server's hosted services ("expose"), or a dynamic SOCKS5/HTTP CONNECT
// proxy for exit/relay-style browsing (the VPN-with-I2P-anonymization mode).
package proxycli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"i2p-proxy/internal/protocol"
	"i2p-proxy/internal/router"
)

type Client struct {
	rt       router.Router
	upstream router.Destination
	log      *slog.Logger
	dialTO   time.Duration
}

func New(rt router.Router, upstream router.Destination, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	// Real I2P dials (multi-hop tunnel build + LeaseSet lookup) commonly
	// take 30-90s even on a well-established router, and longer still for
	// one that's freshly started with no peer reputation yet. 30s proved
	// too tight in practice.
	return &Client{rt: rt, upstream: upstream, log: log, dialTO: 120 * time.Second}
}

// openUpstream dials the remote server over I2P, sends req, and returns the
// stream ready for splicing once the server replies StatusOK.
func (c *Client) openUpstream(ctx context.Context, req protocol.Request) (router.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, c.dialTO)
	defer cancel()

	conn, err := c.rt.Dial(dctx, c.upstream)
	if err != nil {
		return nil, fmt.Errorf("proxycli: dial upstream: %w", err)
	}

	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := protocol.WriteRequest(conn, req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxycli: send request: %w", err)
	}
	status, err := protocol.ReadStatus(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxycli: read status: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	if status != protocol.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxycli: upstream refused: %s", status)
	}
	return conn, nil
}

func splice(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		n, err := io.Copy(a, b)
		slog.Default().Debug("proxycli: splice direction b->a ended", "bytes", n, "err", err)
		done <- struct{}{}
	}()
	go func() {
		n, err := io.Copy(b, a)
		slog.Default().Debug("proxycli: splice direction a->b ended", "bytes", n, "err", err)
		done <- struct{}{}
	}()
	<-done
	a.Close()
	b.Close()
	<-done
}

func acceptLoop(ctx context.Context, ln net.Listener, handle func(context.Context, net.Conn)) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go handle(ctx, c)
	}
}
