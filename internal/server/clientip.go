package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP finds the client address behind trusted proxies (DESIGN.md §8.1), so the IP
// allowlist cannot be bypassed by a forged header.
type ClientIP struct {
	// Header is the forwarding header to read, such as X-Forwarded-For. Empty uses the peer.
	Header string
	// TrustedHops takes the N-th address from the right of the header.
	TrustedHops int
	// TrustedProxies skips addresses in these networks, walking from the right.
	TrustedProxies []netip.Prefix
}

// ParsePrefixes parses CIDRs (or single addresses) for allowlists and trusted proxies.
func ParsePrefixes(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if !strings.Contains(s, "/") {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", s, err)
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// From returns the client address of r.
func (c ClientIP) From(r *http.Request) netip.Addr {
	peer := peerAddr(r.RemoteAddr)
	if c.Header == "" {
		return peer
	}
	var hops []string
	for _, v := range r.Header.Values(c.Header) {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				hops = append(hops, p)
			}
		}
	}
	if len(hops) == 0 {
		return peer
	}
	if len(c.TrustedProxies) > 0 {
		for i := len(hops) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(hops[i])
			if err != nil {
				return peer
			}
			if !c.trusted(a.Unmap()) {
				return a.Unmap()
			}
		}
		return peer
	}
	n := max(c.TrustedHops, 1)
	if n > len(hops) {
		return peer
	}
	a, err := netip.ParseAddr(hops[len(hops)-n])
	if err != nil {
		return peer
	}
	return a.Unmap()
}

func (c ClientIP) trusted(a netip.Addr) bool {
	for _, p := range c.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
