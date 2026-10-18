package proxycli

import (
	"context"
	"fmt"
	"io"
	"net"

	"i2p-proxy/internal/protocol"
)

// SOCKS5 reply codes (RFC 1928 §6).
const (
	socksRepSucceeded           = 0x00
	socksRepGeneralFailure      = 0x01
	socksRepNotAllowed          = 0x02
	socksRepHostUnreachable     = 0x04
	socksRepCommandNotSupported = 0x07
)

// ListenSOCKS5 starts a minimal no-auth SOCKS5 proxy (RFC 1928, CONNECT
// only) on localAddr. Every accepted target is forwarded to the upstream
// server over I2P for it to fulfil (relay to another .i2p destination, or
// exit to clearnet if the operator enabled outproxying).
func (c *Client) ListenSOCKS5(ctx context.Context, localAddr string) error {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}
	c.log.Info("proxycli: socks5 listening", "local", localAddr)

	return acceptLoop(ctx, ln, c.handleSOCKS5)
}

func (c *Client) handleSOCKS5(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if err := socks5Handshake(conn); err != nil {
		c.log.Debug("proxycli: socks5 handshake failed", "err", err)
		return
	}

	req, err := socks5ReadRequest(conn)
	if err != nil {
		c.log.Debug("proxycli: socks5 request failed", "err", err)
		return
	}

	upstream, err := c.openUpstream(ctx, req)
	if err != nil {
		socks5Reply(conn, socksRepHostUnreachable)
		c.log.Warn("proxycli: socks5: upstream failed", "err", err)
		return
	}
	defer upstream.Close()

	if err := socks5Reply(conn, socksRepSucceeded); err != nil {
		return
	}
	splice(conn, upstream)
}

func socks5Handshake(conn net.Conn) error {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return err
	}
	if hdr[0] != 0x05 {
		return fmt.Errorf("socks5: unsupported version %d", hdr[0])
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	// Only "no authentication required" (0x00) is offered; we don't support
	// username/password or GSSAPI.
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}
	return nil
}

func socks5ReadRequest(conn net.Conn) (protocol.Request, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return protocol.Request{}, err
	}
	if hdr[0] != 0x05 {
		return protocol.Request{}, fmt.Errorf("socks5: unsupported version %d", hdr[0])
	}
	if hdr[1] != 0x01 { // CONNECT only
		socks5Reply(conn, socksRepCommandNotSupported)
		return protocol.Request{}, fmt.Errorf("socks5: unsupported command %d", hdr[1])
	}

	var atype protocol.AddrType
	var addr string
	switch hdr[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return protocol.Request{}, err
		}
		addr = net.IP(b).String()
		atype = protocol.AddrIPv4
	case 0x03: // domain name
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return protocol.Request{}, err
		}
		b := make([]byte, lb[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return protocol.Request{}, err
		}
		addr = string(b)
		if isI2PAddr(addr) {
			atype = protocol.AddrI2P
		} else {
			atype = protocol.AddrDomain
		}
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return protocol.Request{}, err
		}
		addr = net.IP(b).String()
		atype = protocol.AddrIPv6
	default:
		socks5Reply(conn, socksRepGeneralFailure)
		return protocol.Request{}, fmt.Errorf("socks5: unsupported address type %d", hdr[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return protocol.Request{}, err
	}
	port := uint16(portBuf[0])<<8 | uint16(portBuf[1])

	return protocol.Request{AddrType: atype, Addr: addr, Port: port}, nil
}

func socks5Reply(conn net.Conn, rep byte) error {
	// BND.ADDR/BND.PORT are meaningless for our use (we don't expose a
	// distinct bind address); send 0.0.0.0:0 per common practice.
	_, err := conn.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func isI2PAddr(host string) bool {
	const suffix = ".i2p"
	return len(host) > len(suffix) && host[len(host)-len(suffix):] == suffix
}
