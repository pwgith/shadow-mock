package config_test

import (
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/config"
)

func TestTLSSettingsAreChecked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want string // empty: accepted
	}{
		{"https alone", "tls: { port: 8443 }", ""},
		{"https only", "tls: { port: 8443, disableHttp: true }", ""},
		{"a keystore", "tls: { port: 8443, keystore: k.p12, keystoreType: PKCS12 }", ""},
		{"a truststore with client certificates required", "tls: { port: 8443, truststore: t.jks, requireClientCert: true }", ""},
		{"PEM files", "tls: { port: 8443, certFile: c.pem, keyFile: k.pem }", ""},
		{"the same port as the plain listener", "tls: { port: 8080 }", "also the port of listen.mock"},
		{"no listener at all", "tls: { disableHttp: true }", "needs tls.port"},
		{"a certificate without a key", "tls: { port: 8443, certFile: c.pem }", "go together"},
		{"two sources for the certificate", "tls: { port: 8443, certFile: c.pem, keyFile: k.pem, keystore: k.p12 }", "use one"},
		{"an unknown keystore type", "tls: { port: 8443, keystore: k, keystoreType: BKS }", "must be JKS or PKCS12"},
		{"client certificates without a truststore", "tls: { port: 8443, requireClientCert: true }", "needs tls.truststore"},
		{"admin over HTTPS with no HTTPS", "tls: { adminRequireHttps: true }", "needs tls.port"},
		{"a keystore with no port", "tls: { keystore: k.p12 }", "need tls.port"},
		{"a port out of range", "tls: { port: 70000 }", "between 0 and 65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			if err := config.Merge(&cfg, []byte(tt.yaml)); err != nil {
				t.Fatal(err)
			}
			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("want it accepted, got %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("want an error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestTheHTTPSAddressFollowsTheBindAddress(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, yaml, want string
	}{
		{"all addresses", "tls: { port: 8443 }", ":8443"},
		{"a bind address", "listen: { bindAddress: 127.0.0.1 }\ntls: { port: 8443 }", "127.0.0.1:8443"},
		{"the mock listener's host", "listen: { mock: '127.0.0.1:9000' }\ntls: { port: 8443 }", "127.0.0.1:8443"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			if err := config.Merge(&cfg, []byte(tt.yaml)); err != nil {
				t.Fatal(err)
			}
			if got := cfg.HTTPSAddr(); got != tt.want {
				t.Errorf("HTTPSAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
