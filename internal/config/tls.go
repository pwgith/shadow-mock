package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// TLS sets the HTTPS listener and HTTP/2 (FR-10.14). Its keys follow WireMock's flags.
type TLS struct {
	// Port turns on an HTTPS listener on that port (--https-port). The listener serves what the
	// mock listener serves.
	Port int `yaml:"port"`
	// CertFile and KeyFile are the certificate chain and private key in PEM. With neither, and no
	// keystore, a self-signed certificate is made at startup, as WireMock does.
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	// Keystore holds the certificate and key as PKCS12 or JKS (--https-keystore).
	Keystore         string `yaml:"keystore"`
	KeystoreType     string `yaml:"keystoreType"`
	KeystorePassword string `yaml:"keystorePassword"`
	// KeyManagerPassword unlocks the key inside a JKS keystore (--key-manager-password).
	KeyManagerPassword string `yaml:"keyManagerPassword"`
	// Truststore holds the certificates of the clients to trust (--https-truststore).
	Truststore         string `yaml:"truststore"`
	TruststoreType     string `yaml:"truststoreType"`
	TruststorePassword string `yaml:"truststorePassword"`
	// RequireClientCert refuses a client that presents no certificate the truststore vouches for.
	RequireClientCert bool `yaml:"requireClientCert"`
	// DisableHTTP leaves only the HTTPS listener (--disable-http).
	DisableHTTP bool `yaml:"disableHttp"`
	// DisableHTTP2Plain and DisableHTTP2TLS turn off HTTP/2 without TLS (h2c) and over TLS.
	DisableHTTP2Plain bool `yaml:"disableHttp2Plain"`
	DisableHTTP2TLS   bool `yaml:"disableHttp2Tls"`
	// AdminRequireHTTPS refuses admin calls that did not arrive over TLS (--admin-api-require-https).
	AdminRequireHTTPS bool `yaml:"adminRequireHttps"`
}

// Enabled reports whether there is an HTTPS listener.
func (t TLS) Enabled() bool { return t.Port > 0 }

// knownKeystoreType reports whether s names a keystore format: JKS or PKCS12 (or a common
// spelling of it). Empty means the default.
func knownKeystoreType(s string) bool {
	switch strings.ToLower(s) {
	case "", "jks", "pkcs12", "pks12", "p12", "pfx":
		return true
	}
	return false
}

func (t TLS) validate() error {
	switch {
	case t.Port < 0 || t.Port > 65535:
		return fmt.Errorf("tls.port must be between 0 and 65535, not %d", t.Port)
	case t.DisableHTTP && !t.Enabled():
		return errors.New("tls.disableHttp needs tls.port: with no HTTP and no HTTPS listener there is nothing to serve")
	case (t.CertFile == "") != (t.KeyFile == ""):
		return errors.New("tls.certFile and tls.keyFile go together")
	case t.CertFile != "" && t.Keystore != "":
		return errors.New("tls.certFile and tls.keystore both give the certificate; use one")
	case !knownKeystoreType(t.KeystoreType):
		return fmt.Errorf("tls.keystoreType must be JKS or PKCS12, not %q", t.KeystoreType)
	case !knownKeystoreType(t.TruststoreType):
		return fmt.Errorf("tls.truststoreType must be JKS or PKCS12, not %q", t.TruststoreType)
	case t.RequireClientCert && t.Truststore == "":
		return errors.New("tls.requireClientCert needs tls.truststore, the certificates to trust")
	case t.AdminRequireHTTPS && !t.Enabled():
		return errors.New("tls.adminRequireHttps needs tls.port: the admin API could not be reached")
	case !t.Enabled() && (t.CertFile != "" || t.Keystore != "" || t.Truststore != ""):
		return errors.New("tls.certFile, tls.keystore and tls.truststore need tls.port")
	}
	return nil
}

// HTTPSAddr is the address of the HTTPS listener.
func (c *Config) HTTPSAddr() string {
	host := c.Listen.BindAddress
	if h, _, err := net.SplitHostPort(c.Listen.Mock); err == nil && host == "" {
		host = h
	}
	return net.JoinHostPort(host, strconv.Itoa(c.TLS.Port))
}

// validateTLS checks the tls section, and that the HTTPS port is not the plain one.
func (c *Config) validateTLS() error {
	if err := c.TLS.validate(); err != nil {
		return err
	}
	if _, port, err := net.SplitHostPort(c.Listen.Mock); err == nil && c.TLS.Enabled() && !c.TLS.DisableHTTP && port == strconv.Itoa(c.TLS.Port) {
		return fmt.Errorf("tls.port %d is also the port of listen.mock; give the HTTPS listener its own", c.TLS.Port)
	}
	return nil
}
