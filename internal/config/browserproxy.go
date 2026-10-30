package config

import (
	"errors"
	"fmt"
	"strings"
)

// BrowserProxy lets a browser or an HTTP client use Shadow Mock as its proxy server (FR-10.15).
// Its keys follow WireMock's flags. It is off unless asked for: a proxy that terminates TLS is
// something to turn on deliberately.
type BrowserProxy struct {
	// Enabled answers absolute-form requests and CONNECT tunnels (--enable-browser-proxying).
	Enabled bool `yaml:"enabled"`
	// CAKeystore holds the certificate authority that signs the certificates made for each host the
	// client tunnels to, as PKCS12 or JKS (--ca-keystore). With none, an authority is made on first
	// use and kept in ~/.shadowmock, with its certificate beside it as ca.pem to import.
	CAKeystore         string `yaml:"caKeystore"`
	CAKeystorePassword string `yaml:"caKeystorePassword"`
	CAKeystoreType     string `yaml:"caKeystoreType"`
	// CADir is where the generated authority is kept; ~/.shadowmock when empty. It is not used when
	// CAKeystore names an authority.
	CADir string `yaml:"caDir"`
	// TrustAll accepts any certificate an origin presents (--trust-all-proxy-targets); TrustTargets
	// accepts any certificate from the named hosts (--trust-proxy-target). Otherwise an origin's
	// certificate must be one the system trusts.
	TrustAll     bool     `yaml:"trustAll"`
	TrustTargets []string `yaml:"trustTargets"`
}

func (b BrowserProxy) validate(via string) error {
	switch {
	case !knownKeystoreType(b.CAKeystoreType):
		return fmt.Errorf("proxy.browser.caKeystoreType must be JKS or PKCS12, not %q", b.CAKeystoreType)
	case !b.Enabled && (b.CAKeystore != "" || b.CADir != "" || b.TrustAll || len(b.TrustTargets) > 0):
		return errors.New("proxy.browser.caKeystore, caDir, trustAll and trustTargets need proxy.browser.enabled")
	}
	if b.Enabled && (b.TrustAll || len(b.TrustTargets) > 0) && via != "" {
		return errors.New("proxy.browser.trustAll and trustTargets cannot be combined with proxy.via: the trust is applied to connections Shadow Mock makes itself")
	}
	for _, t := range b.TrustTargets {
		if strings.TrimSpace(t) == "" || strings.ContainsAny(t, "/ ") {
			return fmt.Errorf("proxy.browser.trustTargets: %q is not a host name", t)
		}
	}
	return nil
}
