// Package protocol implements the small framing format sent once at the
// start of every client -> server I2P stream, telling the server what to
// connect to on the client's behalf. After the frame (and the server's
// status reply), the stream becomes a raw bidirectional byte pipe.
//
// Request wire format:
//
//	byte version (=1)
//	byte addr_type (1=i2p destination, 2=domain, 3=ipv4, 4=ipv6, 5=hosted service)
//	uint16 addr_len big-endian (addr is empty for hosted service requests)
//	bytes addr addr_len bytes
//	uint16 port big-endian (for hosted service requests, this is the
//	 virtual service port the server operator published,
//	 not necessarily the local port it maps to)
//
// Reply wire format:
//
//	byte status (0=ok, 1=denied, 2=unreachable, 3=outproxy-disabled)
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

type AddrType byte

const (
	AddrI2P    AddrType = 1
	AddrDomain AddrType = 2
	AddrIPv4   AddrType = 3
	AddrIPv6   AddrType = 4
	// AddrHosted requests one of the server operator's explicitly published
	// local services (see server.hosted_services in config) - the path used
	// by client "expose" entries. Kept separate from AddrIPv4/AddrDomain so
	// it is never subject to the outproxy ACL's loopback/private-network
	// denylist, which exists specifically to block reaching the operator's
	// localhost/internal network via arbitrary clearnet dials.
	AddrHosted AddrType = 5
)

const Version byte = 1

type Status byte

const (
	StatusOK               Status = 0
	StatusDenied           Status = 1
	StatusUnreachable      Status = 2
	StatusOutproxyDisabled Status = 3
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusDenied:
		return "denied"
	case StatusUnreachable:
		return "unreachable"
	case StatusOutproxyDisabled:
		return "outproxy-disabled"
	default:
		return fmt.Sprintf("status(%d)", byte(s))
	}
}

// Request is the target a client asks the server to connect to.
type Request struct {
	AddrType AddrType
	Addr     string
	Port     uint16
}

const maxAddrLen = 0xFFFF

func WriteRequest(w io.Writer, req Request) error {
	if len(req.Addr) > maxAddrLen {
		return fmt.Errorf("protocol: address too long (%d bytes)", len(req.Addr))
	}
	buf := make([]byte, 4+len(req.Addr)+2)
	buf[0] = Version
	buf[1] = byte(req.AddrType)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(req.Addr)))
	copy(buf[4:], req.Addr)
	binary.BigEndian.PutUint16(buf[4+len(req.Addr):], req.Port)
	_, err := w.Write(buf)
	return err
}

func ReadRequest(r io.Reader) (Request, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Request{}, fmt.Errorf("protocol: reading header: %w", err)
	}
	if hdr[0] != Version {
		return Request{}, fmt.Errorf("protocol: unsupported version %d", hdr[0])
	}
	addrLen := binary.BigEndian.Uint16(hdr[2:4])
	addrBuf := make([]byte, addrLen)
	if _, err := io.ReadFull(r, addrBuf); err != nil {
		return Request{}, fmt.Errorf("protocol: reading address: %w", err)
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(r, portBuf[:]); err != nil {
		return Request{}, fmt.Errorf("protocol: reading port: %w", err)
	}
	return Request{
		AddrType: AddrType(hdr[1]),
		Addr:     string(addrBuf),
		Port:     binary.BigEndian.Uint16(portBuf[:]),
	}, nil
}

func WriteStatus(w io.Writer, s Status) error {
	_, err := w.Write([]byte{byte(s)})
	return err
}

func ReadStatus(r io.Reader) (Status, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, fmt.Errorf("protocol: reading status: %w", err)
	}
	return Status(b[0]), nil
}
