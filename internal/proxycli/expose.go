package proxycli

import (
	"context"
	"net"

	"i2p-proxy/internal/protocol"
)

// Expose listens on localAddr and, per accepted connection, tunnels to the
// server's hosted service published under remotePort (protocol.AddrHosted).
// This is the "browse the remote eepsite's service on localhost:PORT like
// it's local" mode.
func (c *Client) Expose(ctx context.Context, localAddr string, remotePort int) error {
	ln, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}
	c.log.Info("proxycli: exposing hosted service", "local", localAddr, "remote_port", remotePort)

	return acceptLoop(ctx, ln, func(ctx context.Context, local net.Conn) {
		defer local.Close()
		upstream, err := c.openUpstream(ctx, protocol.Request{
			AddrType: protocol.AddrHosted,
			Port:     uint16(remotePort),
		})
		if err != nil {
			c.log.Warn("proxycli: expose: upstream failed", "err", err)
			return
		}
		splice(local, upstream)
	})
}
