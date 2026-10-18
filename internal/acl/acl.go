// Package acl gates what the server side is allowed to connect out to on a
// client's behalf: port allowlisting and SSRF-style network denylisting for
// the outproxy/exit path, plus a simple per-source connection counter for
// rate limiting.
package acl

import (
	"fmt"
	"net"
	"sync"
)

// DefaultDenyNetworks blocks private/loopback/link-local ranges so the
// outproxy path can't be used to pivot into the operator's internal network.
func DefaultDenyNetworks() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

type Policy struct {
	OutproxyEnabled bool
	RelayToI2P      bool
	AllowPorts      []int
	DenyNetworks    []*net.IPNet
}

func (p Policy) PortAllowed(port int) bool {
	if len(p.AllowPorts) == 0 {
		return true
	}
	for _, ap := range p.AllowPorts {
		if ap == port {
			return true
		}
	}
	return false
}

// HostAllowed checks a resolved clearnet IP against the deny list. Callers
// must resolve DNS themselves and check every returned address - checking
// only the hostname string is not sufficient (DNS rebinding).
func (p Policy) HostAllowed(ip net.IP) error {
	for _, n := range p.DenyNetworks {
		if n.Contains(ip) {
			return fmt.Errorf("acl: target %s is in a denied network (%s)", ip, n)
		}
	}
	return nil
}

// RateLimiter caps concurrent connections per source I2P destination.
type RateLimiter struct {
	maxPerSource int

	mu     sync.Mutex
	counts map[string]int
}

func NewRateLimiter(maxPerSource int) *RateLimiter {
	return &RateLimiter{maxPerSource: maxPerSource, counts: make(map[string]int)}
}

// Acquire returns a release func on success, or ok=false if the source is
// at its connection cap.
func (rl *RateLimiter) Acquire(source string) (release func(), ok bool) {
	if rl.maxPerSource <= 0 {
		return func() {}, true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.counts[source] >= rl.maxPerSource {
		return nil, false
	}
	rl.counts[source]++
	return func() {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		rl.counts[source]--
		if rl.counts[source] <= 0 {
			delete(rl.counts, source)
		}
	}, true
}
