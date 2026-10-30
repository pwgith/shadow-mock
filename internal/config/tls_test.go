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

func TestBrowserProxySettingsAreChecked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want string // empty: accepted
	}{
		{"on", "proxy: { browser: { enabled: true } }", ""},
		{"with its own authority", "proxy: { browser: { enabled: true, caKeystore: ca.p12, caKeystoreType: PKCS12 } }", ""},
		{"trusting an origin", "proxy: { browser: { enabled: true, trustTargets: [example.com] } }", ""},
		{"an authority without browser proxying", "proxy: { browser: { caKeystore: ca.p12 } }", "need proxy.browser.enabled"},
		{"trust without browser proxying", "proxy: { browser: { trustAll: true } }", "need proxy.browser.enabled"},
		{"an unknown keystore type", "proxy: { browser: { enabled: true, caKeystoreType: BKS } }", "must be JKS or PKCS12"},
		{"a trusted target that is not a host", "proxy: { browser: { enabled: true, trustTargets: ['http://x/'] } }", "not a host name"},
		{"trust with an upstream proxy", "proxy: { via: 'p:3128', browser: { enabled: true, trustAll: true } }", "cannot be combined with proxy.via"},
		{"an upstream proxy without trust", "proxy: { via: 'p:3128', browser: { enabled: true } }", ""},
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

func TestBrowserProxyFlagsSetTheConfiguration(t *testing.T) {
	t.Parallel()
	cfg, _, err := config.Resolve([]string{
		"--enable-browser-proxying", "--ca-keystore", "ca.jks", "--ca-keystore-password", "pw", "--ca-keystore-type", "jks",
		"--trust-all-proxy-targets", "--trust-proxy-target", "a.example,b.example", "--trust-proxy-target", "c.example",
	}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Proxy.Browser
	if !b.Enabled || !b.TrustAll || b.CAKeystore != "ca.jks" || b.CAKeystorePassword != "pw" || b.CAKeystoreType != "jks" ||
		strings.Join(b.TrustTargets, ",") != "a.example,b.example,c.example" {
		t.Errorf("flags not applied: %+v", b)
	}
}

func TestExtensionSettingsAreChecked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want string // empty: accepted
	}{
		{"all three", "templating: { extensions: [jwt, faker] }\nmatching: { extensions: [graphql] }", ""},
		{"jwt keys with jwt on", "templating: { extensions: [jwt], jwt: { hs256Secret: s, rs256PrivateKey: k, rs256PublicKey: p } }", ""},
		{"an unknown template extension", "templating: { extensions: [nope] }", "templating.extensions"},
		{"graphql among the template extensions", "templating: { extensions: [graphql] }", "templating.extensions"},
		{"jwt among the matcher extensions", "matching: { extensions: [jwt] }", "matching.extensions"},
		{"jwt keys without jwt", "templating: { jwt: { hs256Secret: s } }", "needs jwt in templating.extensions"},
		{"a public key without a private one", "templating: { extensions: [jwt], jwt: { rs256PublicKey: p } }", "needs templating.jwt.rs256PrivateKey"},
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

func TestTheExtensionsFlagTurnsOnTheExtensionsThatReplaceTheJavaOnes(t *testing.T) {
	t.Parallel()
	cfg, flags, err := config.Resolve([]string{
		"--extensions", "org.wiremock.extension.jwt.JwtExtensionFactory,org.wiremock.RandomExtension,io.github.nilwurtz.GraphqlBodyMatcher,com.example.Mine",
	}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Templating.Extensions, ",") != "jwt,faker" || strings.Join(cfg.Matching.Extensions, ",") != "graphql" {
		t.Errorf("extensions = %v and %v", cfg.Templating.Extensions, cfg.Matching.Extensions)
	}
	if len(flags.Warnings) != 1 || !strings.Contains(flags.Warnings[0], "com.example.Mine") || strings.Contains(flags.Warnings[0], "JwtExtensionFactory") {
		t.Errorf("want one warning about the unknown class only, got %v", flags.Warnings)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the configuration the flag makes is not valid: %v", err)
	}
}
