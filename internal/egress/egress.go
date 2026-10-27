// Package egress builds the HTTP client used for every outbound call the engine makes on a
// mapping's behalf: webhooks and, later, proxying. It always has timeouts. In SaaS it also
// refuses private, loopback and link-local addresses, so a mapping cannot be pointed at
// the platform's own network (NFR-5.3, CODING_STANDARDS.md §12).
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrBlocked is returned, wrapped, when a destination is not allowed.
var ErrBlocked = errors.New("egress_blocked")

// Options configure the client.
type Options struct {
	// BlockPrivate refuses non-public destination addresses.
	BlockPrivate bool
	// Timeout caps one whole call. Zero means 30 seconds.
	Timeout time.Duration
}

// New returns a client for outbound calls. It does not follow redirects, so a target cannot
// bounce a call to somewhere the policy would not have allowed.
func New(o Options) *http.Client {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if o.BlockPrivate {
		d.Control = control
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        64,
			IdleConnTimeout:     60 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// control runs after DNS resolution, so a hostname that resolves to a private address is
// refused as well as a literal one.
func control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot read destination %q", ErrBlocked, address)
	}
	if Blocked(ap.Addr()) {
		return fmt.Errorf("%w: %s is not a public address", ErrBlocked, ap.Addr())
	}
	return nil
}

// Blocked reports whether an address is one the SaaS policy refuses: loopback, private, link-local,
// multicast and unspecified addresses, plus ranges that are not public even though the standard
// library does not call them private, such as the carrier-grade NAT range that some clouds use
// for their metadata service. Addresses that wrap an IPv4 address (NAT64 and 6to4) are judged by
// the address inside.
func Blocked(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range reserved() {
		if p.Contains(a) {
			return true
		}
	}
	if a.Is6() {
		b := a.As16()
		switch {
		case netip.MustParsePrefix("64:ff9b::/96").Contains(a): // NAT64
			return Blocked(netip.AddrFrom4([4]byte(b[12:16])))
		case netip.MustParsePrefix("2002::/16").Contains(a): // 6to4
			return Blocked(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	return false
}

// reserved lists ranges that are not for public use and that the netip helpers do not cover.
func reserved() []netip.Prefix {
	ranges := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "100::/64", "2001:db8::/32", "fec0::/10",
	}
	out := make([]netip.Prefix, 0, len(ranges))
	for _, s := range ranges {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}
