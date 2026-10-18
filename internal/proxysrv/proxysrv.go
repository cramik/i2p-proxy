// Package proxysrv is the server side: it accepts inbound I2P streams on
// the local Destination (the eepsite), reads the protocol.Request each
// client sends, and either relays to another I2P destination or - if the
// operator has opted in - exits to clearnet (outproxy/VPN-style egress).
package proxysrv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"i2p-proxy/internal/acl"
	"i2p-proxy/internal/protocol"
	"i2p-proxy/internal/router"
)

type Options struct {
	Policy      acl.Policy
	RateLimiter *acl.RateLimiter
	// HostedServices maps a virtual port to a local address for the
	// AddrHosted request path (client "expose" entries).
	HostedServices map[int]string
	// LogDestinations controls whether source/target addresses are logged.
	// Off by default per the project's anonymity-preserving logging policy.
	LogDestinations bool
	DialTimeout     time.Duration
}

type Server struct {
	rt   router.Router
	opts Options
	log  *slog.Logger
}

func New(rt router.Router, opts Options, log *slog.Logger) *Server {
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 30 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{rt: rt, opts: opts, log: log}
}

// Run accepts connections until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := s.rt.Listen(ctx)
	if err != nil {
		return fmt.Errorf("proxysrv: listen: %w", err)
	}
	defer ln.Close()

	s.log.Info("proxysrv: listening", "destination", redact(string(ln.LocalDestination()), s.opts.LogDestinations))

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
				return fmt.Errorf("proxysrv: accept: %w", err)
			}
		}
		go s.handle(ctx, c)
	}
}

func (s *Server) handle(ctx context.Context, c router.Conn) {
	defer c.Close()

	source := string(c.RemoteDestination())
	release, ok := s.rateAcquire(source)
	if !ok {
		s.log.Warn("proxysrv: rate limit exceeded", "source", redact(source, s.opts.LogDestinations))
		return
	}
	defer release()

	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	req, err := protocol.ReadRequest(c)
	if err != nil {
		s.log.Debug("proxysrv: bad request", "err", err)
		return
	}
	_ = c.SetDeadline(time.Time{})

	upstream, status := s.dial(ctx, req)
	if err := protocol.WriteStatus(c, status); err != nil {
		return
	}
	if status != protocol.StatusOK {
		s.log.Info("proxysrv: request denied", "status", status, "target", redact(targetString(req), s.opts.LogDestinations))
		return
	}
	defer upstream.Close()

	s.log.Info("proxysrv: relaying", "target", redact(targetString(req), s.opts.LogDestinations))
	splice(c, upstream)
}

func (s *Server) rateAcquire(source string) (func(), bool) {
	if s.opts.RateLimiter == nil {
		return func() {}, true
	}
	return s.opts.RateLimiter.Acquire(source)
}

// dial resolves and connects to the requested target, applying ACL policy.
// Returns an io.ReadWriteCloser so both I2P relay hops (router.Conn) and
// clearnet exits (net.Conn) satisfy the same splice path.
func (s *Server) dial(ctx context.Context, req protocol.Request) (io.ReadWriteCloser, protocol.Status) {
	dctx, cancel := context.WithTimeout(ctx, s.opts.DialTimeout)
	defer cancel()

	switch req.AddrType {
	case protocol.AddrI2P:
		if !s.opts.Policy.RelayToI2P {
			return nil, protocol.StatusOutproxyDisabled
		}
		conn, err := s.rt.Dial(dctx, router.Destination(req.Addr))
		if err != nil {
			return nil, protocol.StatusUnreachable
		}
		return conn, protocol.StatusOK

	case protocol.AddrDomain, protocol.AddrIPv4, protocol.AddrIPv6:
		if !s.opts.Policy.OutproxyEnabled {
			return nil, protocol.StatusOutproxyDisabled
		}
		if !s.opts.Policy.PortAllowed(int(req.Port)) {
			return nil, protocol.StatusDenied
		}
		return s.dialClearnet(dctx, req)

	case protocol.AddrHosted:
		local, ok := s.opts.HostedServices[int(req.Port)]
		if !ok {
			return nil, protocol.StatusDenied
		}
		var d net.Dialer
		conn, err := d.DialContext(dctx, "tcp", local)
		if err != nil {
			return nil, protocol.StatusUnreachable
		}
		return conn, protocol.StatusOK

	default:
		return nil, protocol.StatusDenied
	}
}

func (s *Server) dialClearnet(ctx context.Context, req protocol.Request) (io.ReadWriteCloser, protocol.Status) {
	var d net.Dialer
	addr := net.JoinHostPort(req.Addr, strconv.Itoa(int(req.Port)))

	// Resolve first so we can ACL-check every resolved IP (blocks DNS
	// rebinding around the deny list), then dial that specific IP.
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, req.Addr)
	if err != nil {
		if ip := net.ParseIP(req.Addr); ip != nil {
			ips = []net.IPAddr{{IP: ip}}
		} else {
			return nil, protocol.StatusUnreachable
		}
	}
	var chosen net.IP
	for _, ipa := range ips {
		if err := s.opts.Policy.HostAllowed(ipa.IP); err != nil {
			continue
		}
		chosen = ipa.IP
		break
	}
	if chosen == nil {
		return nil, protocol.StatusDenied
	}

	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(chosen.String(), strconv.Itoa(int(req.Port))))
	if err != nil {
		_ = addr
		return nil, protocol.StatusUnreachable
	}
	return conn, protocol.StatusOK
}

func splice(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		n, err := io.Copy(a, b)
		slog.Default().Debug("proxysrv: splice direction b->a ended", "bytes", n, "err", err)
		done <- struct{}{}
	}()
	go func() {
		n, err := io.Copy(b, a)
		slog.Default().Debug("proxysrv: splice direction a->b ended", "bytes", n, "err", err)
		done <- struct{}{}
	}()
	<-done
	a.Close()
	b.Close()
	<-done
}

func targetString(req protocol.Request) string {
	return net.JoinHostPort(req.Addr, strconv.Itoa(int(req.Port)))
}

func redact(s string, log bool) string {
	if log {
		return s
	}
	return "<redacted>"
}
