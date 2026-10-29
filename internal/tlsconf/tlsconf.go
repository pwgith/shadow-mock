// Package tlsconf builds the TLS settings of the HTTPS listener (FR-10.14) from the tls section of
// the configuration: a certificate from PEM files, from a PKCS12 or JKS keystore as WireMock takes
// them, or made on the spot; and, when clients must prove themselves, the certificates to trust.
package tlsconf

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
)

// defaultPassword is what WireMock assumes for a keystore, truststore and key when none is given.
const defaultPassword = "password"

// Config returns the TLS settings for the listener. Over TLS the server speaks HTTP/1.1, and
// HTTP/2 unless the configuration turns it off, which http.Server arranges from its Protocols.
func Config(c config.TLS, clk clock.Clock) (*tls.Config, error) {
	cert, err := certificate(c, clk)
	if err != nil {
		return nil, err
	}
	out := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if c.Truststore == "" {
		return out, nil
	}
	pool, err := trusted(c)
	if err != nil {
		return nil, err
	}
	out.ClientCAs = pool
	out.ClientAuth = tls.VerifyClientCertIfGiven
	if c.RequireClientCert {
		out.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return out, nil
}

func certificate(c config.TLS, clk clock.Clock) (tls.Certificate, error) {
	switch {
	case c.CertFile != "":
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("tls.certFile and tls.keyFile: %w", err)
		}
		return cert, nil
	case c.Keystore != "":
		cert, err := fromKeystore(c)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("tls.keystore %s: %w", c.Keystore, err)
		}
		return cert, nil
	}
	return SelfSigned(clk)
}

func password(s string) string {
	if s == "" {
		return defaultPassword
	}
	return s
}

// isJKS reports whether data is a Java keystore, by its magic number. A keystore with no type given
// is read as WireMock reads it, as JKS, unless it is plainly something else.
func isJKS(kind string, data []byte) bool {
	switch strings.ToLower(kind) {
	case "jks":
		return true
	case "":
		return len(data) >= 4 && binary.BigEndian.Uint32(data) == 0xFEEDFEED
	}
	return false
}

func fromKeystore(c config.TLS) (tls.Certificate, error) {
	data, err := os.ReadFile(c.Keystore)
	if err != nil {
		return tls.Certificate{}, err
	}
	storePass := password(c.KeystorePassword)
	if isJKS(c.KeystoreType, data) {
		keyPass := c.KeyManagerPassword
		if keyPass == "" {
			keyPass = storePass
		}
		return jksCertificate(data, storePass, keyPass)
	}
	key, leaf, chain, err := pkcs12.DecodeChain(data, storePass)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read PKCS12: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return tls.Certificate{}, errors.New("the key is not one that can sign")
	}
	cert := tls.Certificate{PrivateKey: signer, Leaf: leaf, Certificate: [][]byte{leaf.Raw}}
	for _, ca := range chain {
		cert.Certificate = append(cert.Certificate, ca.Raw)
	}
	return cert, nil
}

func jksCertificate(data []byte, storePass, keyPass string) (tls.Certificate, error) {
	ks := keystore.New()
	if err := ks.Load(bytes.NewReader(data), []byte(storePass)); err != nil {
		return tls.Certificate{}, fmt.Errorf("read JKS (is keystore-password right?): %w", err)
	}
	for _, alias := range ks.Aliases() {
		if !ks.IsPrivateKeyEntry(alias) {
			continue
		}
		e, err := ks.GetPrivateKeyEntry(alias, []byte(keyPass))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("open key %q (is key-manager-password right?): %w", alias, err)
		}
		key, err := x509.ParsePKCS8PrivateKey(e.PrivateKey)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("read key %q: %w", alias, err)
		}
		signer, ok := key.(crypto.Signer)
		if !ok || len(e.CertificateChain) == 0 {
			return tls.Certificate{}, fmt.Errorf("key %q has no usable certificate", alias)
		}
		cert := tls.Certificate{PrivateKey: signer}
		for _, c := range e.CertificateChain {
			cert.Certificate = append(cert.Certificate, c.Content)
		}
		return cert, nil
	}
	return tls.Certificate{}, errors.New("the keystore holds no private key")
}

// trusted reads the certificates a client may be vouched for by.
func trusted(c config.TLS) (*x509.CertPool, error) {
	data, err := os.ReadFile(c.Truststore)
	if err != nil {
		return nil, fmt.Errorf("tls.truststore: %w", err)
	}
	pass := password(c.TruststorePassword)
	pool := x509.NewCertPool()
	if isJKS(c.TruststoreType, data) {
		return pool, addJKSCertificates(pool, data, pass)
	}
	certs, err := pkcs12.DecodeTrustStore(data, pass)
	if err != nil {
		return nil, fmt.Errorf("tls.truststore: read PKCS12: %w", err)
	}
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
}

// SelfSigned makes the certificate used when none is configured, valid for localhost and the
// loopback addresses, as WireMock's built-in one is for local use.
func SelfSigned(clk clock.Clock) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make serial number: %w", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost", Organization: []string{"Shadow Mock"}},
		NotBefore:    clk.Now().Add(-time.Hour),
		NotAfter:     clk.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("make certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// addJKSCertificates adds the trusted certificates of a JKS truststore to pool.
func addJKSCertificates(pool *x509.CertPool, data []byte, pass string) error {
	ks := keystore.New()
	if err := ks.Load(bytes.NewReader(data), []byte(pass)); err != nil {
		return fmt.Errorf("tls.truststore: read JKS (is truststore-password right?): %w", err)
	}
	for _, alias := range ks.Aliases() {
		if !ks.IsTrustedCertificateEntry(alias) {
			continue
		}
		e, err := ks.GetTrustedCertificateEntry(alias)
		if err != nil {
			return fmt.Errorf("tls.truststore: %w", err)
		}
		cert, err := x509.ParseCertificate(e.Certificate.Content)
		if err != nil {
			return fmt.Errorf("tls.truststore: certificate %q: %w", alias, err)
		}
		pool.AddCert(cert)
	}
	return nil
}
