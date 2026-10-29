package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path"
	"strings"
	"time"
)

// ErrDenied is returned, wrapped, when proxy target rules refuse a destination.
var ErrDenied = errors.New("target_denied")

// Targets decides which hosts may be proxied to or recorded from (WireMock's --allow-proxy-targets
// and --deny-proxy-targets). A rule is an IP address, a range of addresses written a-b, a CIDR
// block, or a name that may contain * as a wildcard. The allowed list is looked at first: when
// it is not empty, a target it does not name is refused. The denied list is looked at next.
type Targets struct {
	allow, deny []rule
}

// rule matches a host by name or by address.
type rule struct {
	name     string // a name or wildcard, lower case
	from, to netip.Addr
	prefix   netip.Prefix
}

// ParseTargets builds target rules from the two lists.
func ParseTargets(allow, deny []string) (*Targets, error) {
	t := &Targets{}
	var err error
	if t.allow, err = parseRules(allow); err != nil {
		return nil, fmt.Errorf("allow: %w", err)
	}
	if t.deny, err = parseRules(deny); err != nil {
		return nil, fmt.Errorf("deny: %w", err)
	}
	return t, nil
}

func parseRules(specs []string) ([]rule, error) {
	out := make([]rule, 0, len(specs))
	for _, s := range specs {
		s = strings.TrimSpace(s)
		switch {
		case s == "":
		case strings.Contains(s, "/"):
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return nil, fmt.Errorf("%q is not a CIDR block: %w", s, err)
			}
			out = append(out, rule{prefix: p})
		case isRange(s):
			lo, hi, _ := strings.Cut(s, "-")
			from, err1 := netip.ParseAddr(strings.TrimSpace(lo))
			to, err2 := netip.ParseAddr(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || from.Compare(to) > 0 {
				return nil, fmt.Errorf("%q is not a range of addresses, from low to high", s)
			}
			out = append(out, rule{from: from, to: to})
		default:
			if a, err := netip.ParseAddr(s); err == nil {
				out = append(out, rule{from: a, to: a})
				continue
			}
			out = append(out, rule{name: strings.ToLower(s)})
		}
	}
	return out, nil
}

// isRange reports whether s looks like two addresses joined by a hyphen.
func isRange(s string) bool {
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		return false
	}
	_, e1 := netip.ParseAddr(strings.TrimSpace(lo))
	_, e2 := netip.ParseAddr(strings.TrimSpace(hi))
	return e1 == nil && e2 == nil
}

// Empty reports whether there are no rules, so every target is allowed.
func (t *Targets) Empty() bool { return t == nil || (len(t.allow) == 0 && len(t.deny) == 0) }

// Check reports an error wrapping ErrDenied when the rules refuse host, a name or an address.
func (t *Targets) Check(ctx context.Context, host string) error {
	if t.Empty() {
		return nil
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	addrs := addressesOf(ctx, host, t.needsAddresses())
	if len(t.allow) > 0 && !anyMatch(t.allow, host, addrs) {
		return fmt.Errorf("%w: %s is not in the allowed targets", ErrDenied, host)
	}
	if anyMatch(t.deny, host, addrs) {
		return fmt.Errorf("%w: %s is in the denied targets", ErrDenied, host)
	}
	return nil
}

// needsAddresses reports whether any rule is about addresses, in which case a name is resolved.
func (t *Targets) needsAddresses() bool {
	for _, rs := range [][]rule{t.allow, t.deny} {
		for _, r := range rs {
			if r.name == "" {
				return true
			}
		}
	}
	return false
}

// addressesOf returns the addresses of host: itself when it is one, else what it resolves to
// when resolve is set.
func addressesOf(ctx context.Context, host string, resolve bool) []netip.Addr {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}
	}
	if !resolve {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func anyMatch(rules []rule, host string, addrs []netip.Addr) bool {
	for _, r := range rules {
		if r.name != "" {
			if ok, _ := path.Match(r.name, host); ok { //nolint:errcheck // a bad pattern matches nothing
				return true
			}
			continue
		}
		for _, a := range addrs {
			if r.prefix.IsValid() && r.prefix.Contains(a) {
				return true
			}
			if r.from.IsValid() && a.Compare(r.from) >= 0 && a.Compare(r.to) <= 0 {
				return true
			}
		}
	}
	return false
}
