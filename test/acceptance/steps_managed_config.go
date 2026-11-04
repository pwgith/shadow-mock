package acceptance

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/auth/authtest"
)

// initManagedConfig registers the steps that start managed mode from a configuration file, to see what start-up accepts.
func (w *world) initManagedConfig(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is configured for managed mode$`, func() error {
		w.cfgDocs = append(w.cfgDocs, w.validManagedYAML())
		return nil
	})
	sc.Step(`^templating\.extensions is "jwt" with the RS256 and HS256 keys set$`, func() error {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return fmt.Errorf("make a key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return fmt.Errorf("encode the key: %w", err)
		}
		pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		w.cfgDocs = append(w.cfgDocs, "templating:\n  extensions: [jwt]\n  jwt:\n    hs256Secret: a-secret-all-instances-share\n    rs256PrivateKey: |\n"+indent(pemKey, "      "))
		return nil
	})
	sc.Step(`^start-up succeeds$`, func() error {
		if w.cfgErr != nil {
			return fmt.Errorf("start-up failed: %w", w.cfgErr)
		}
		return nil
	})
	sc.Step(`^start-up fails$`, func() error {
		if w.cfgErr == nil {
			return errors.New("start-up succeeded, want a failure")
		}
		return nil
	})
}

// validManagedYAML is a configuration that managed mode accepts: a pinned issuer, global writes closed, and one
// instance on in-memory stores.
func (w *world) validManagedYAML() string {
	issuer := authtest.NewIssuer("management-app", managedIssuerURL, managedAudience, nil)
	return "mode: managed\n" +
		"managed: { singleInstance: true }\n" +
		"admin: { globalWrites: deny }\n" +
		"mappings: { unknownFields: keep }\n" +
		"egress: { blockPrivate: true }\n" +
		"auth:\n  issuers:\n    - name: management-app\n      issuer: " + managedIssuerURL + "\n      audience: " + managedAudience + "\n      publicKey: |\n" +
		indent(issuer.PublicPEM(), "        ")
}
