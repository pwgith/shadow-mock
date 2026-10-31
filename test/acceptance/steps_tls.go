package acceptance

import (
	"bytes"
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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/pwgith/shadow-mock/internal/tlsconf"
)

// initTLS registers the steps that make key material for the HTTPS listener and its clients (FR-10.14).
func (w *world) initTLS(sc *godog.ScenarioContext) {
	sc.Step(`^a PKCS12 keystore with password "([^"]*)" is available as "([^"]*)"$`, func(pass, name string) error {
		cert, leaf, err := w.serverCert()
		if err != nil {
			return err
		}
		data, err := pkcs12.Modern.Encode(cert.PrivateKey, leaf, nil, pass)
		if err != nil {
			return fmt.Errorf("encode the keystore: %w", err)
		}
		return w.keyFile(name, name+".p12", data)
	})
	sc.Step(`^a JKS keystore with password "([^"]*)" is available as "([^"]*)"$`, func(pass, name string) error {
		cert, leaf, err := w.serverCert()
		if err != nil {
			return err
		}
		key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			return fmt.Errorf("encode the key: %w", err)
		}
		ks := keystore.New()
		entry := keystore.PrivateKeyEntry{
			CreationTime: time.Now(), PrivateKey: key,
			CertificateChain: []keystore.Certificate{{Type: "X509", Content: leaf.Raw}},
		}
		if err = ks.SetPrivateKeyEntry("server", entry, []byte(pass)); err != nil {
			return fmt.Errorf("add the key: %w", err)
		}
		var buf bytes.Buffer
		if err = ks.Store(&buf, []byte(pass)); err != nil {
			return fmt.Errorf("store the keystore: %w", err)
		}
		return w.keyFile(name, name+".jks", buf.Bytes())
	})
	sc.Step(`^a PEM certificate and key are available as "([^"]*)" and "([^"]*)"$`, func(certName, keyName string) error {
		cert, leaf, err := w.serverCert()
		if err != nil {
			return err
		}
		key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			return fmt.Errorf("encode the key: %w", err)
		}
		if certErr := w.keyFile(certName, certName+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})); certErr != nil {
			return certErr
		}
		return w.keyFile(keyName, keyName+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	})
	sc.Step(`^a client certificate is trusted through the (PKCS12|JKS) truststore "([^"]*)" with password "([^"]*)"$`, w.trustClient)
	sc.Step(`^requests are sent with the client certificate$`, func() error {
		if w.clientCert == nil {
			return errors.New("no client certificate has been made")
		}
		return w.clientCertificates([]tls.Certificate{*w.clientCert})
	})
	sc.Step(`^requests are sent with no client certificate$`, func() error { return w.clientCertificates(nil) })
	sc.Step(`^starting Shadow Mock with configuration fails with an error mentioning "([^"]*)":$`, func(part string, doc *godog.DocString) error {
		if err := w.stop(); err != nil {
			return err
		}
		w.yamls = append(w.yamls, doc.Content)
		err := w.start()
		switch {
		case err == nil:
			return errors.New("start-up succeeded, want a failure")
		case !strings.Contains(err.Error(), part):
			return fmt.Errorf("start-up failed with %q, want it to mention %q", err, part)
		}
		return nil
	})
	sc.Step(`^the request fails because the server turned the client away$`, func() error {
		switch {
		case w.sendErr == nil:
			return errors.New("the request got a response, want it to fail")
		case !turnedAway.MatchString(w.sendErr.Error()):
			return fmt.Errorf("the request failed with %q, want a refused client certificate", w.sendErr)
		}
		return nil
	})
	sc.Step(`^the request fails with an error mentioning "([^"]*)"$`, func(part string) error {
		switch {
		case w.sendErr == nil:
			return errors.New("the request got a response, want it to fail")
		case !strings.Contains(w.sendErr.Error(), part):
			return fmt.Errorf("the request failed with %q, want it to mention %q", w.sendErr, part)
		}
		return nil
	})
}

// turnedAway is how a client sees a server refuse its certificate: an alert, or a connection closed
// before the alert is read, or an HTTP/2 client that never got its connection up, depending on the timing.
var turnedAway = regexp.MustCompile(`(?i)certificate|forcibly closed|reset|EOF|client conn could not be established`)

// serverCert makes a certificate for the server's key material.
func (w *world) serverCert() (tls.Certificate, *x509.Certificate, error) {
	cert, err := tlsconf.SelfSigned(w.clk)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("make a certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read the certificate: %w", err)
	}
	return cert, leaf, nil
}

// makeClientCert makes a self-signed certificate that may be used to identify a client.
func (w *world) makeClientCert() (tls.Certificate, *x509.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("make a key: %w", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "acceptance client"},
		NotBefore:    w.clk.Now().Add(-time.Hour),
		NotAfter:     w.clk.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("make a certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read the certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, leaf, nil
}

// keyFile writes key material to the scenario's directory and names its path for ${name}.
func (w *world) keyFile(name, file string, data []byte) error {
	path := filepath.Join(w.root, file)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	w.vars[name] = path
	return nil
}

// trustClient makes a client certificate and a truststore that holds it.
func (w *world) trustClient(kind, name, pass string) error {
	cert, leaf, err := w.makeClientCert()
	if err != nil {
		return err
	}
	w.clientCert = &cert
	if kind == "PKCS12" {
		data, encErr := pkcs12.Modern.EncodeTrustStore([]*x509.Certificate{leaf}, pass)
		if encErr != nil {
			return fmt.Errorf("encode the truststore: %w", encErr)
		}
		return w.keyFile(name, name+".p12", data)
	}
	ks := keystore.New()
	entry := keystore.TrustedCertificateEntry{CreationTime: time.Now(), Certificate: keystore.Certificate{Type: "X509", Content: leaf.Raw}}
	if err = ks.SetTrustedCertificateEntry("client", entry); err != nil {
		return fmt.Errorf("add the certificate: %w", err)
	}
	var buf bytes.Buffer
	if err = ks.Store(&buf, []byte(pass)); err != nil {
		return fmt.Errorf("store the truststore: %w", err)
	}
	return w.keyFile(name, name+".jks", buf.Bytes())
}

// clientCertificates sets the certificates the scenario's client offers when a server asks for them.
func (w *world) clientCertificates(certs []tls.Certificate) error {
	tr, ok := w.client.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		return errors.New("the scenario's client cannot hold a client certificate")
	}
	tr.TLSClientConfig.Certificates = certs
	tr.CloseIdleConnections()
	return nil
}
