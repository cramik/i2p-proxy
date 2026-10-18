package proxycli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"i2p-proxy/internal/protocol"
)

// ListenHTTP starts a minimal HTTP proxy on localAddr supporting CONNECT
// (for HTTPS tunneling, and generically for any TCP protocol) and absolute-
// form HTTP/1.x requests (plain proxied HTTP). Like SOCKS5, every target is
// handed to the upstream server over I2P to fulfil.
func (c *Client) ListenHTTP(ctx context.Context, localAddr string) error {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}
	c.log.Info("proxycli: http proxy listening", "local", localAddr)

	return acceptLoop(ctx, ln, c.handleHTTP)
}

func (c *Client) handleHTTP(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method == http.MethodConnect {
		c.handleConnect(ctx, conn, req.Host)
		return
	}
	c.handlePlainHTTP(ctx, conn, br, req)
}

func (c *Client) handleConnect(ctx context.Context, conn net.Conn, target string) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		host, portStr = target, "443"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	upstream, err := c.openUpstream(ctx, protocol.Request{
		AddrType: addrTypeFor(host),
		Addr:     host,
		Port:     uint16(port),
	})
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		c.log.Warn("proxycli: http connect: upstream failed", "err", err)
		return
	}
	defer upstream.Close()

	if _, err := fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	splice(conn, upstream)
}

func (c *Client) handlePlainHTTP(ctx context.Context, conn net.Conn, br *bufio.Reader, req *http.Request) {
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	hostname, portStr, err := net.SplitHostPort(host)
	if err != nil {
		hostname, portStr = host, "80"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	upstream, err := c.openUpstream(ctx, protocol.Request{
		AddrType: addrTypeFor(hostname),
		Addr:     hostname,
		Port:     uint16(port),
	})
	if err != nil {
		fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		c.log.Warn("proxycli: http: upstream failed", "err", err)
		return
	}
	defer upstream.Close()

	// Rewrite absolute-form request URI to origin-form before forwarding -
	// the upstream dials the origin server directly, which expects a normal
	// (non-proxy) HTTP request.
	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	if err := req.Write(upstream); err != nil {
		return
	}

	// Splice any remaining buffered/pipelined bytes both ways.
	splice(bufConn{Reader: br, Conn: conn}, upstream)
}

func addrTypeFor(host string) protocol.AddrType {
	if isI2PAddr(host) {
		return protocol.AddrI2P
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return protocol.AddrIPv4
		}
		return protocol.AddrIPv6
	}
	return protocol.AddrDomain
}

// bufConn lets a bufio.Reader's buffered leftovers be spliced along with
// the underlying net.Conn's writes/close.
type bufConn struct {
	*bufio.Reader
	net.Conn
}

func (b bufConn) Read(p []byte) (int, error) { return b.Reader.Read(p) }
