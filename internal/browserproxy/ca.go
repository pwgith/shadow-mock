// Package browserproxy lets a browser or an HTTP client use Shadow Mock as its proxy server
// (FR-10.15). An HTTP request sent to the proxy in absolute form is answered like any mock request.
// For HTTPS the client sends CONNECT, and Shadow Mock ends the TLS itself with a certificate it makes
// for the host and signs with a certificate authority the client has been told to trust, then
// answers the requests inside the tunnel the same way. Nothing is relayed unread: a tunnel is
// always opened up, so a mapping can match what is inside it.
package browserproxy

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/tlsconf"
)

const (
	// defaultPassword is the password of the generated authority's keystore, WireMock's default.
	defaultPassword = "password"
	// maxLeaves bounds the certificates kept; a client that tunnels to many hosts cannot grow it.
	maxLeaves = 1024
	// leafLife is how long a certificate made for a host is good for.
	leafLife = 30 * 24 * time.Hour
	// caKeystoreName and caPEMName are the files of the generated authority.
	caKeystoreName = "ca-keystore.p12"
	caPEMName      = "ca.pem"
)

// CA is a certificate authority that signs the certificates Shadow Mock presents to a proxied
// client, one for each host.
type CA struct {
	cert   *x509.Certificate
	signer crypto.Signer
	clk    clock.Clock

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// NewCA wraps a certificate and key as an authority. The certificate must be one that may sign others.
func NewCA(c tls.Certificate, clk clock.Clock) (*CA, error) {
	if len(c.Certificate) == 0 {
		return nil, errors.New("the keystore holds no certificate")
	}
	cert, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("read the certificate authority: %w", err)
	}
	signer, ok := c.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("the authority's key is not one that can sign")
	}
	if !cert.IsCA {
		return nil, errors.New("the certificate in the keystore is not a certificate authority (it must have CA:TRUE)")
	}
	return &CA{cert: cert, signer: signer, clk: clk, leaves: map[string]*tls.Certificate{}}, nil
}

// Load returns the authority the configuration names, or the one kept in dir, making and storing it
// first if there is none. dir is where the generated authority lives: its keystore and ca.pem.
func Load(c config.BrowserProxy, dir string, clk clock.Clock) (*CA, error) {
	if c.CAKeystore != "" {
		cert, err := tlsconf.LoadKeystore(c.CAKeystore, c.CAKeystoreType, c.CAKeystorePassword, "")
		if err != nil {
			return nil, fmt.Errorf("proxy.browser.caKeystore %s: %w", c.CAKeystore, err)
		}
		return NewCA(cert, clk)
	}
	keystore := filepath.Join(dir, caKeystoreName)
	if _, err := os.Stat(keystore); err == nil {
		cert, loadErr := tlsconf.LoadKeystore(keystore, "pkcs12", defaultPassword, "")
		if loadErr != nil {
			return nil, fmt.Errorf("%s: %w", keystore, loadErr)
		}
		return NewCA(cert, clk)
	}
	return generate(dir, clk)
}

// generate makes an authority and stores it, with its certificate as PEM for importing into a
// browser or a trust store.
func generate(dir string, clk clock.Clock) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("make the authority's key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Shadow Mock browser proxy CA", Organization: []string{"Shadow Mock"}},
		NotBefore:             clk.Now().Add(-time.Hour),
		NotAfter:              clk.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("make the authority's certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("read the authority's certificate: %w", err)
	}
	store, err := pkcs12.Modern.Encode(key, cert, nil, defaultPassword)
	if err != nil {
		return nil, fmt.Errorf("encode the authority's keystore: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, caKeystoreName), store, 0o600); err != nil {
		return nil, fmt.Errorf("store the authority: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, caPEMName), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil { //nolint:gosec // the certificate is public; it is what a client imports
		return nil, fmt.Errorf("store the authority's certificate: %w", err)
	}
	return NewCA(tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, clk)
}

func newSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		return nil, fmt.Errorf("make a serial number: %w", err)
	}
	return serial, nil
}

// Certificate is the authority's own certificate, the one a client has to trust.
func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

// PEM is the authority's certificate in PEM.
func (ca *CA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// Leaf returns a certificate for host (a name or an IP address) signed by the authority. A certificate
// is made once and kept until it nears its end.
func (ca *CA) Leaf(host string) (*tls.Certificate, error) {
	now := ca.clk.Now()
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.leaves[host]; ok && c.Leaf.NotAfter.After(now.Add(time.Hour)) {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("make a key for %s: %w", host, err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Shadow Mock"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.signer)
	if err != nil {
		return nil, fmt.Errorf("make a certificate for %s: %w", host, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("read the certificate for %s: %w", host, err)
	}
	if len(ca.leaves) >= maxLeaves {
		clear(ca.leaves)
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	ca.leaves[host] = c
	return c, nil
}
