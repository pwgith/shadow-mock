package auth

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientAddrPrefersTheResolvedAddress(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		remote string
		ctxIP  string
		want   string
	}{
		{"the address the server resolved", "10.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"the peer when nothing was resolved", "10.0.0.1:5000", "", "10.0.0.1"},
		{"an IPv6 peer", "[2001:db8::1]:443", "", "2001:db8::1"},
		{"a peer that is not host:port", "pipe", "", "pipe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			if tt.ctxIP != "" {
				r = r.WithContext(WithClientIP(r.Context(), netip.MustParseAddr(tt.ctxIP)))
			}
			if got := clientAddr(r); got != tt.want {
				t.Errorf("clientAddr = %q, want %q", got, tt.want)
			}
		})
	}
}
