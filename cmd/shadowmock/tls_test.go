package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/pwgith/shadow-mock/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// serveWith runs the server in the background with the TLS settings change makes, and returns the
// plain and the HTTPS base URLs once they answer.
func serveWith(t *testing.T, change func(*config.TLS)) (plain, secure string) {
	t.Helper()
	return serveCfg(t, func(c *config.Config) { change(&c.TLS) })
}

// serveCfg is serveWith for a change to any part of the configuration.
func serveCfg(t *testing.T, change func(*config.Config)) (plain, secure string) {
	t.Helper()
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Storage.Global.Watch = false
	httpPort, httpsPort := strconv.Itoa(freePort(t)), strconv.Itoa(freePort(t))
	cfg.Listen.Mock = net.JoinHostPort("127.0.0.1", httpPort)
	cfg.TLS.Port, _ = strconv.Atoi(httpsPort)
	change(&cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	quit, done := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	stop := func() { once.Do(func() { close(quit) }) }
	go func() { done <- serveUntil(quit, stop, cfg, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	waitFor(t, "127.0.0.1:"+httpsPort)
	if !cfg.TLS.DisableHTTP {
		waitFor(t, "127.0.0.1:"+httpPort)
	}
	return "http://127.0.0.1:" + httpPort, "https://127.0.0.1:" + httpsPort
}

func waitFor(t *testing.T, addr string) {
	t.Helper()
	for range 100 {
		c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nothing is listening on %s", addr)
}

// client returns an HTTP client that trusts any server certificate. With h2c it speaks HTTP/2 on
// prior knowledge to a plain address, and only that, as a Go client otherwise prefers HTTP/1.1 there.
func client(t *testing.T, certs []tls.Certificate, h2TLS, h2c bool) *http.Client {
	t.Helper()
	var p http.Protocols
	p.SetHTTP1(!h2c)
	p.SetHTTP2(h2TLS)
	p.SetUnencryptedHTTP2(h2c)
	tr := &http.Transport{
		Protocols:       &p,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: certs},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

// answer is what a request got back: the parts of the response the tests look at.
type answer struct {
	StatusCode int
	ProtoMajor int
	Proto      string
}

func get(t *testing.T, c *http.Client, url string) (*answer, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return &answer{StatusCode: resp.StatusCode, ProtoMajor: resp.ProtoMajor, Proto: resp.Proto}, string(b), nil
}

func TestHTTPSAnswersWithHTTP2ByDefault(t *testing.T) {
	t.Parallel()
	plain, secure := serveWith(t, func(*config.TLS) {})
	resp, _, err := get(t, client(t, nil, true, false), secure+"/__health")
	if err != nil || resp.ProtoMajor != 2 {
		t.Fatalf("HTTPS: want HTTP/2, got %v, %v", resp, err)
	}
	resp, _, err = get(t, client(t, nil, false, true), plain+"/__health")
	if err != nil || resp.ProtoMajor != 2 {
		t.Fatalf("plain with prior knowledge: want HTTP/2, got %v, %v", resp, err)
	}
	resp, _, err = get(t, client(t, nil, false, false), secure+"/__health")
	if err != nil || resp.ProtoMajor != 1 || resp.StatusCode != http.StatusOK {
		t.Fatalf("an HTTP/1.1 client: got %v, %v", resp, err)
	}
}

func TestHTTP2CanBeSwitchedOff(t *testing.T) {
	t.Parallel()
	plain, secure := serveWith(t, func(c *config.TLS) { c.DisableHTTP2TLS, c.DisableHTTP2Plain = true, true })
	if resp, _, err := get(t, client(t, nil, true, false), secure+"/__health"); err != nil || resp.ProtoMajor != 1 {
		t.Errorf("HTTPS with HTTP/2 off: want HTTP/1.1, got %v, %v", resp, err)
	}
	if resp, _, err := get(t, client(t, nil, false, true), plain+"/__health"); err == nil {
		t.Errorf("h2c with HTTP/2 off should fail, got %v", resp.Proto)
	}
	if resp, _, err := get(t, client(t, nil, false, false), plain+"/__health"); err != nil || resp.ProtoMajor != 1 {
		t.Errorf("plain HTTP/1.1 must still work: %v, %v", resp, err)
	}
}

func TestDisableHTTPLeavesOnlyHTTPS(t *testing.T) {
	t.Parallel()
	plain, secure := serveWith(t, func(c *config.TLS) { c.DisableHTTP = true })
	c := client(t, nil, true, false)
	if _, _, err := get(t, c, plain+"/__health"); err == nil {
		t.Error("the plain port should refuse connections")
	}
	if resp, _, err := get(t, c, secure+"/__health"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("HTTPS: got %v, %v", resp, err)
	}
}

func TestAdminRequireHTTPS(t *testing.T) {
	t.Parallel()
	plain, secure := serveWith(t, func(c *config.TLS) { c.AdminRequireHTTPS = true })
	c := client(t, nil, true, false)
	resp, body, err := get(t, c, plain+"/__admin/mappings")
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain admin call: want 403, got %v, %v", resp, err)
	}
	var got, want any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	if err := json.Unmarshal([]byte(`{"errors":[{"code":40,"title":"HTTPS is required for accessing the admin API"}]}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body %s, want WireMock's %v", body, want)
	}
	if resp, _, err := get(t, c, secure+"/__admin/mappings"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("HTTPS admin call: got %v, %v", resp, err)
	}
	if resp, _, err := get(t, c, plain+"/nothing"); err != nil || resp.StatusCode == http.StatusForbidden {
		t.Errorf("mock traffic is not limited to HTTPS: got %v, %v", resp, err)
	}
}

// clientCert makes a certificate a client can present, and a PKCS12 truststore that vouches for it.
func clientCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "test client"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	store, err := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{leaf}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trust.p12")
	if err := os.WriteFile(path, store, 0o600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

func TestClientCertificatesAreRequiredWhenAsked(t *testing.T) {
	t.Parallel()
	cert, trust := clientCert(t)
	_, secure := serveWith(t, func(c *config.TLS) {
		c.Truststore, c.TruststoreType, c.TruststorePassword, c.RequireClientCert = trust, "PKCS12", "pw", true
	})
	if _, _, err := get(t, client(t, nil, true, false), secure+"/__health"); err == nil {
		t.Error("a client with no certificate should be turned away")
	}
	other, _ := clientCert(t)
	if _, _, err := get(t, client(t, []tls.Certificate{other}, true, false), secure+"/__health"); err == nil {
		t.Error("a client certificate the truststore does not hold should be turned away")
	}
	if resp, _, err := get(t, client(t, []tls.Certificate{cert}, true, false), secure+"/__health"); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("a trusted client certificate: got %v, %v", resp, err)
	}
}
