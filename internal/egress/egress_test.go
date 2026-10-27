package egress_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/egress"
)

func TestBlocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"fe80::1", true},
		{"fd00::1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::ffff:127.0.0.1", true}, // IPv4 mapped into IPv6
		{"93.184.216.34", false},
		{"2606:2800:220:1:248:1893:25c8:1946", false},
	}
	for _, tt := range tests {
		if got := egress.Blocked(netip.MustParseAddr(tt.addr)); got != tt.want {
			t.Errorf("Blocked(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestClientBlocksPrivateDestinations(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()

	open := egress.New(egress.Options{Timeout: 5 * time.Second})
	resp, err := open.Get(srv.URL) //nolint:noctx // a plain call is enough to test the client
	if err != nil {
		t.Fatalf("a client that allows private addresses failed: %v", err)
	}
	resp.Body.Close()

	closed := egress.New(egress.Options{BlockPrivate: true, Timeout: 5 * time.Second})
	_, err = closed.Get(srv.URL) //nolint:noctx,bodyclose // the call is expected to fail
	if !errors.Is(err, egress.ErrBlocked) {
		t.Errorf("a loopback call with BlockPrivate returned %v, want ErrBlocked", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := egress.New(egress.Options{}).Get(srv.URL) //nolint:noctx // a plain call is enough to test the client
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want the redirect itself (302)", resp.StatusCode)
	}
}
