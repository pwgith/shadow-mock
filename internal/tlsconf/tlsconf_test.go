package tlsconf_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/config"
	"github.com/pwgith/shadow-mock/internal/tlsconf"
)

// material is a certificate and key made for a test, and the files they are kept in.
type material struct {
	cert tls.Certificate
	leaf *x509.Certificate
	dir  string
}

func newMaterial(t *testing.T) material {
	t.Helper()
	cert, err := tlsconf.SelfSigned(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return material{cert: cert, leaf: leaf, dir: t.TempDir()}
}

func (m material) write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(m.dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (m material) pkcs12(t *testing.T, pass string) string {
	t.Helper()
	data, err := pkcs12.Modern.Encode(m.cert.PrivateKey, m.leaf, nil, pass)
	if err != nil {
		t.Fatal(err)
	}
	return m.write(t, "k-"+pass+".p12", data)
}

func (m material) jks(t *testing.T, storePass, keyPass string) string {
	t.Helper()
	pk, err := x509.MarshalPKCS8PrivateKey(m.cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	ks := keystore.New()
	err = ks.SetPrivateKeyEntry("server", keystore.PrivateKeyEntry{
		CreationTime:     time.Now(),
		PrivateKey:       pk,
		CertificateChain: []keystore.Certificate{{Type: "X509", Content: m.leaf.Raw}},
	}, []byte(keyPass))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := ks.Store(&b, []byte(storePass)); err != nil {
		t.Fatal(err)
	}
	return m.write(t, "k-"+storePass+"-"+keyPass+".jks", []byte(b.String()))
}

func served(t *testing.T, c *tls.Config) *x509.Certificate {
	t.Helper()
	if c == nil || len(c.Certificates) != 1 {
		t.Fatalf("want one certificate, got %+v", c)
	}
	leaf, err := x509.ParseCertificate(c.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func TestWithNoCertificateOneIsMadeForLocalhost(t *testing.T) {
	t.Parallel()
	c, err := tlsconf.Config(config.TLS{Port: 1}, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	leaf := served(t, c)
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Error(err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Error(err)
	}
}

func TestACertificateIsReadFromPEMFiles(t *testing.T) {
	t.Parallel()
	m := newMaterial(t)
	certPEM := m.write(t, "c.pem", pemOf("CERTIFICATE", m.leaf.Raw))
	keyDER, err := x509.MarshalPKCS8PrivateKey(m.cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := m.write(t, "k.pem", pemOf("PRIVATE KEY", keyDER))
	c, err := tlsconf.Config(config.TLS{Port: 1, CertFile: certPEM, KeyFile: keyPEM}, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	if !served(t, c).Equal(m.leaf) {
		t.Error("the certificate is not the one in the file")
	}
}

func TestACertificateIsReadFromAKeystore(t *testing.T) {
	t.Parallel()
	m := newMaterial(t)
	tests := []struct {
		name string
		cfg  config.TLS
	}{
		{"PKCS12", config.TLS{Keystore: m.pkcs12(t, "s3cret"), KeystoreType: "PKCS12", KeystorePassword: "s3cret"}},
		{"PKCS12 found without a type", config.TLS{Keystore: m.pkcs12(t, "password"), KeystoreType: "PKCS12"}},
		{"JKS", config.TLS{Keystore: m.jks(t, "s3cret", "s3cret"), KeystoreType: "JKS", KeystorePassword: "s3cret"}},
		{"JKS with its own key password", config.TLS{Keystore: m.jks(t, "a", "b"), KeystorePassword: "a", KeyManagerPassword: "b"}},
		{"JKS with WireMock's default passwords", config.TLS{Keystore: m.jks(t, "password", "password")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.cfg.Port = 1
			c, err := tlsconf.Config(tt.cfg, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			if !served(t, c).Equal(m.leaf) {
				t.Error("the certificate is not the one in the keystore")
			}
		})
	}
}

func TestAWrongKeystorePasswordIsSaidSo(t *testing.T) {
	t.Parallel()
	m := newMaterial(t)
	for name, cfg := range map[string]config.TLS{
		"PKCS12":       {Keystore: m.pkcs12(t, "right"), KeystoreType: "PKCS12", KeystorePassword: "wrong"},
		"JKS":          {Keystore: m.jks(t, "right", "right"), KeystorePassword: "wrong"},
		"JKS key only": {Keystore: m.jks(t, "right", "key"), KeystorePassword: "right", KeyManagerPassword: "wrong"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg.Port = 1
			_, err := tlsconf.Config(cfg, clock.Real{})
			if err == nil || !strings.Contains(err.Error(), "tls.keystore") {
				t.Errorf("want an error naming the keystore, got %v", err)
			}
		})
	}
}

func TestClientCertificatesAreVerifiedAgainstTheTruststore(t *testing.T) {
	t.Parallel()
	client := newMaterial(t)
	p12, err := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{client.leaf}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	ks := keystore.New()
	if err := ks.SetTrustedCertificateEntry("client", keystore.TrustedCertificateEntry{
		CreationTime: time.Now(), Certificate: keystore.Certificate{Type: "X509", Content: client.leaf.Raw},
	}); err != nil {
		t.Fatal(err)
	}
	var jks strings.Builder
	if err := ks.Store(&jks, []byte("pw")); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]config.TLS{
		"PKCS12": {Truststore: client.write(t, "t.p12", p12), TruststoreType: "PKCS12", TruststorePassword: "pw"},
		"JKS":    {Truststore: client.write(t, "t.jks", []byte(jks.String())), TruststorePassword: "pw"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg.Port = 1
			c, err := tlsconf.Config(cfg, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			if c.ClientAuth != tls.VerifyClientCertIfGiven {
				t.Errorf("a truststore alone asks for a certificate but does not insist: got %v", c.ClientAuth)
			}
			if _, verifyErr := client.leaf.Verify(x509.VerifyOptions{Roots: c.ClientCAs, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); verifyErr != nil {
				t.Errorf("the truststore does not hold the client certificate: %v", verifyErr)
			}
			cfg.RequireClientCert = true
			c, err = tlsconf.Config(cfg, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			if c.ClientAuth != tls.RequireAndVerifyClientCert {
				t.Errorf("requireClientCert must insist, got %v", c.ClientAuth)
			}
		})
	}
}

func TestMissingFilesAreReported(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]config.TLS{
		"keystore":   {Keystore: "/nonexistent.p12"},
		"truststore": {Truststore: "/nonexistent.jks"},
		"pem":        {CertFile: "/nonexistent.pem", KeyFile: "/nonexistent.key"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg.Port = 1
			if _, err := tlsconf.Config(cfg, clock.Real{}); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func pemOf(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}
