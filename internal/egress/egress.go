// Package egress builds the HTTP client used for every outbound call the engine makes on a
// mapping's behalf: webhooks and, later, proxying. It always has timeouts. In SaaS it also
// refuses private, loopback and link-local addresses, so a mapping cannot be pointed at
// the platform's own network (NFR-5.3, CODING_STANDARDS.md §12).
package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
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
	// Proxy, when set, picks a proxy server to send each call through; see ViaProxy.
	Proxy func(*http.Request) (*url.URL, error)
	// TrustAll accepts any certificate a server presents; TrustedHosts accepts any from those hosts.
	// Otherwise a server's certificate must be one the system trusts.
	TrustAll     bool
	TrustedHosts []string
}

// ViaProxy returns the Proxy option for a proxy server written as host:port or as a URL.
func ViaProxy(via string) (func(*http.Request) (*url.URL, error), error) {
	raw := via
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the proxy server %q is not host:port or a URL", via)
	}
	return http.ProxyURL(u), nil
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
	tr := &http.Transport{
		Proxy:               o.Proxy,
		DialContext:         d.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        64,
		IdleConnTimeout:     60 * time.Second,
	}
	if o.trusting() {
		tr.DialTLSContext, tr.ForceAttemptHTTP2 = o.dialTLS(d), true
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// trusting reports whether some servers are trusted without checking their certificate.
func (o Options) trusting() bool { return o.TrustAll || len(o.TrustedHosts) > 0 }

// dialTLS dials a server and ends up with a TLS connection to it. The check of the server's certificate
// is made here, where the host that was dialled is known, because Go does not say it to a verifier
// when it is an IP address.
func (o Options) dialTLS(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	hosts := make([]string, len(o.TrustedHosts))
	for i, h := range o.TrustedHosts {
		hosts[i] = strings.ToLower(h)
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("read address %q: %w", addr, err)
		}
		raw, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		trusted := o.TrustAll || slices.Contains(hosts, strings.ToLower(host))
		conn := tls.Client(raw, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", "http/1.1"},
			// The default check is switched off so that it can be skipped for the servers that are trusted,
			// and made, with the same rules, for every other.
			InsecureSkipVerify: true, //nolint:gosec // VerifyConnection below verifies the chain of every server that is not trusted
			VerifyConnection: func(cs tls.ConnectionState) error {
				if trusted {
					return nil
				}
				return verifyChain(cs, host)
			},
		})
		hsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := conn.HandshakeContext(hsCtx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return conn, nil
	}
}

// verifyChain checks a server's certificate chain against the system's roots for host.
func verifyChain(cs tls.ConnectionState, host string) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("the server presented no certificate")
	}
	opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, c := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(opts)
	return err
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
