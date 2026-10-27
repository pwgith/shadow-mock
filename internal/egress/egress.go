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

// Blocked reports whether an address is one the SaaS policy refuses.
func Blocked(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || !a.IsValid()
}
