package egress_test

import (
	"errors"
	"testing"

	"github.com/pwgith/shadow-mock/internal/egress"
)

func TestTargetRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		allow, deny []string
		host        string
		denied      bool
	}{
		{"no rules allow everything", nil, nil, "example.com", false},
		{"an address on the deny list", nil, []string{"127.0.0.1"}, "127.0.0.1", true},
		{"an address not on it", nil, []string{"127.0.0.1"}, "127.0.0.2", false},
		{"a range", nil, []string{"10.0.0.1-10.0.0.9"}, "10.0.0.5", true},
		{"outside the range", nil, []string{"10.0.0.1-10.0.0.9"}, "10.0.0.10", false},
		{"a CIDR block", nil, []string{"192.168.0.0/16"}, "192.168.4.4", true},
		{"a name", nil, []string{"localhost"}, "LocalHost", true},
		{"a wildcard name", nil, []string{"*.internal.example"}, "db.internal.example", true},
		{"a wildcard that does not match", nil, []string{"*.internal.example"}, "example.com", false},
		{"allowed only what the allow list names", []string{"*.example.com"}, nil, "api.example.com", false},
		{"not on the allow list", []string{"*.example.com"}, nil, "evil.test", true},
		{"deny wins over allow", []string{"*.example.com"}, []string{"admin.example.com"}, "admin.example.com", true},
		{"an IPv6 address in brackets", nil, []string{"::1"}, "[::1]", true},
		{"an address written as a mapped IPv4", nil, []string{"127.0.0.1"}, "::ffff:127.0.0.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			targets, err := egress.ParseTargets(tt.allow, tt.deny)
			if err != nil {
				t.Fatal(err)
			}
			err = targets.Check(t.Context(), tt.host)
			if tt.denied != errors.Is(err, egress.ErrDenied) {
				t.Errorf("Check(%q) = %v, want denied = %v", tt.host, err, tt.denied)
			}
		})
	}
}

func TestTargetRulesRefuseWhatTheyCannotRead(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"10.0.0.9-10.0.0.1", "10.0.0.0/33"} {
		if _, err := egress.ParseTargets(nil, []string{bad}); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	var none *egress.Targets
	if err := none.Check(t.Context(), "anything"); err != nil {
		t.Errorf("no rules at all: %v", err)
	}
}

func TestViaProxy(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"proxy.local:3128", "http://proxy.local:3128"} {
		if f, err := egress.ViaProxy(ok); f == nil || err != nil {
			t.Errorf("%q: set=%v, %v", ok, f != nil, err)
		}
	}
	for _, bad := range []string{"://", ""} {
		if _, err := egress.ViaProxy(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if _, err := egress.ViaProxy("://"); err == nil {
		t.Error("a proxy address that is not one must be refused")
	}
}
