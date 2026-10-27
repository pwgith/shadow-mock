package tenant_test

import (
	"net/netip"
	"testing"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

func TestContext(t *testing.T) {
	t.Parallel()
	if got := tenant.From(t.Context()); got != tenant.Local {
		t.Errorf("From(empty context) = %q, want %q", got, tenant.Local)
	}
	ctx := tenant.With(t.Context(), "acme")
	if got := tenant.From(ctx); got != "acme" {
		t.Errorf("From(With(acme)) = %q", got)
	}
}

func TestOrgAllows(t *testing.T) {
	t.Parallel()
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}
	tests := []struct {
		name      string
		allowlist []netip.Prefix
		ip        string
		want      bool
	}{
		{"an empty list allows everyone", nil, "203.0.113.7", true},
		{"inside an IPv4 network", prefixes, "10.1.2.3", true},
		{"outside every network", prefixes, "192.168.1.1", false},
		{"inside an IPv6 network", prefixes, "2001:db8::1", true},
		{"an IPv4-mapped IPv6 address is unmapped", prefixes, "::ffff:10.1.2.3", true},
		{"an IPv4-mapped address outside the networks", prefixes, "::ffff:192.0.2.1", false},
		{"IPv6 outside", prefixes, "2001:4860::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			org := &tenant.Org{ID: "acme", Handle: "acme", Allowlist: tt.allowlist}
			if got := org.Allows(netip.MustParseAddr(tt.ip)); got != tt.want {
				t.Errorf("Allows(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestStaticDirectory(t *testing.T) {
	t.Parallel()
	acme := &tenant.Org{ID: "org_1", Handle: "acme"}
	globex := &tenant.Org{ID: "org_2", Handle: "globex"}
	d := tenant.NewStatic(acme, globex)
	if got, ok := d.Lookup("acme"); !ok || got != acme {
		t.Errorf("Lookup(acme) = %v, %v", got, ok)
	}
	if got, ok := d.Lookup("globex"); !ok || got.ID != "org_2" {
		t.Errorf("Lookup(globex) = %v, %v", got, ok)
	}
	if _, ok := d.Lookup("initech"); ok {
		t.Error("Lookup of an unknown handle should fail")
	}
}
