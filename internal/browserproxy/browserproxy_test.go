package browserproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
)

func newCA(t *testing.T) (*CA, string) {
	t.Helper()
	dir := t.TempDir()
	ca, err := Load(config.BrowserProxy{Enabled: true}, dir, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	return ca, dir
}

func TestAnAuthorityIsMadeOnceAndKept(t *testing.T) {
	t.Parallel()
	first, dir := newCA(t)
	for _, name := range []string{caKeystoreName, caPEMName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("want %s stored: %v", name, err)
		}
	}
	second, err := Load(config.BrowserProxy{Enabled: true}, dir, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Certificate().Equal(second.Certificate()) {
		t.Error("a second start made another authority instead of using the stored one")
	}
	pemData, err := os.ReadFile(filepath.Join(dir, caPEMName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pemData, first.PEM()) {
		t.Error("ca.pem is not the authority's certificate")
	}
}

func TestACertificateForAHostIsSignedByTheAuthority(t *testing.T) {
	t.Parallel()
	ca, _ := newCA(t)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Certificate())
	for _, host := range []string{"example.com", "localhost", "127.0.0.1", "::1", "a-b_c.example.org"} {
		c, err := ca.Leaf(host)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
			t.Errorf("%s: a client that trusts the authority would refuse the certificate: %v", host, err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "other.example"}); err == nil {
			t.Errorf("%s: the certificate must not be good for another name", host)
		}
	}
}

func TestACertificateIsKeptUntilItNearsItsEnd(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ca, err := Load(config.BrowserProxy{Enabled: true}, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ca.Leaf("example.com")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := ca.Leaf("example.com")
	if first != again {
		t.Error("a second request for the same host made another certificate")
	}
	clk.Advance(leafLife)
	renewed, _ := ca.Leaf("example.com")
	if renewed == first {
		t.Error("a certificate at the end of its life was handed out again")
	}
}

func TestTheCertificatesKeptAreBounded(t *testing.T) {
	t.Parallel()
	ca, _ := newCA(t)
	for i := range maxLeaves + 5 {
		if _, err := ca.Leaf("h" + strings.Repeat("x", i%50) + ".example"); err != nil {
			t.Fatal(err)
		}
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if len(ca.leaves) > maxLeaves {
		t.Errorf("%d certificates kept, the limit is %d", len(ca.leaves), maxLeaves)
	}
}

func TestAKeystoreThatIsNotAnAuthorityIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A leaf certificate stored as the authority: made by an authority, so it has CA:FALSE.
	ca, _ := newCA(t)
	leaf, err := ca.Leaf("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCA(*leaf, clock.Real{}); err == nil || !strings.Contains(err.Error(), "not a certificate authority") {
		t.Errorf("want a refusal naming the problem, got %v", err)
	}
	if _, err := Load(config.BrowserProxy{Enabled: true, CAKeystore: filepath.Join(dir, "missing.p12")}, dir, clock.Real{}); err == nil || !strings.Contains(err.Error(), "caKeystore") {
		t.Errorf("a missing keystore must be reported by its setting, got %v", err)
	}
	if _, err := NewCA(tls.Certificate{}, clock.Real{}); err == nil {
		t.Error("an empty keystore must be refused")
	}
}

func TestOnlyAHostAndPortCanBeTunnelledTo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		host string
		ok   bool
	}{
		{"example.com:443", "example.com", true},
		{"127.0.0.1:8443", "127.0.0.1", true},
		{"[::1]:443", "::1", true},
		{"example.com", "", false},
		{"example.com:0", "", false},
		{"example.com:70000", "", false},
		{"example.com:https", "", false},
		{":443", "", false},
		{"exa mple.com:443", "", false},
		{"*.example.com:443", "", false},
		{"ex\r\nample.com:443", "", false},
	}
	for _, tt := range tests {
		host, ok := target(tt.in)
		if host != tt.host || ok != tt.ok {
			t.Errorf("target(%q) = %q, %v; want %q, %v", tt.in, host, ok, tt.host, tt.ok)
		}
	}
}
