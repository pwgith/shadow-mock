package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/auth"
)

// IssuerConfig names a trusted issuer of admin tokens (FR-11.2). Trust exists only here, never in the
// database.
type IssuerConfig struct {
	// Name labels the issuer in the audit trail.
	Name string `yaml:"name"`
	// Issuer is the exact `iss` the issuer writes, and Audience the exact `aud` this environment expects.
	Issuer   string `yaml:"issuer"`
	Audience string `yaml:"audience"`
	// JWKSURL is where the issuer publishes its keys, read again to pick up rotation. PublicKey pins one
	// key in PEM instead. Give one of the two.
	JWKSURL   string `yaml:"jwksUrl"`
	PublicKey string `yaml:"publicKey"`
	// MaxLifetime caps how long a token may live, whatever the issuer says. Zero is two minutes.
	MaxLifetime Duration `yaml:"maxLifetime"`
	// ReplayCheck makes each token usable once.
	ReplayCheck bool `yaml:"replayCheck"`
	// RoleMap maps the `app_role` claim to admin, editor or viewer. Empty maps admin, member and viewer.
	RoleMap map[string]string `yaml:"roleMap"`
}

// Managed holds the settings of managed mode (FR-11.1).
type Managed struct {
	// SingleInstance lets managed mode run on in-memory stores, on one instance. It is for development,
	// tests and trials: without it managed mode needs the postgres, redis and s3 drivers, so that several
	// instances behave as one (FR-11.1). Any of the three can still be set on a single instance.
	SingleInstance bool `yaml:"singleInstance"`
}

// Console is the embedded web console (FR-11.17). It is not built yet; managed mode refuses it.
type Console struct {
	Enabled bool `yaml:"enabled"`
}

// maxTokenLifetime is the longest cap an operator may set on a token.
const maxTokenLifetime = 10 * time.Minute

// validateManaged applies the rules of managed mode (FR-11.1). It runs only in that mode.
func (c *Config) validateManaged() error {
	if c.Mode != "managed" {
		if c.Managed.SingleInstance {
			return errors.New("managed.singleInstance needs mode: managed")
		}
		if len(c.Auth.Issuers) > 0 {
			return errors.New("auth.issuers needs mode: managed")
		}
		return nil
	}
	var errs []error
	add := func(msg string) { errs = append(errs, errors.New(msg)) }
	if len(c.Auth.Issuers) == 0 {
		add("mode: managed needs auth.issuers: the admin API is closed until a trusted issuer is named")
	}
	names := map[string]bool{}
	for i, is := range c.Auth.Issuers {
		errs = append(errs, is.validate(fmt.Sprintf("auth.issuers[%d]", i), names))
	}
	switch {
	case c.Admin.GlobalWrites != "deny":
		add("mode: managed needs admin.globalWrites: deny, because the management app writes the global layer")
	case c.Mappings.UnknownFields != "keep":
		add("mode: managed needs mappings.unknownFields: keep, because the management app stores fields the engine does not know")
	case !c.Egress.BlockPrivate:
		add("mode: managed needs egress.blockPrivate: true, and nothing may turn it off")
	case c.Proxy.Browser.Enabled:
		add("mode: managed refuses proxy.browser: a shared host must not be an open proxy")
	case c.TLS.Enabled():
		add("mode: managed refuses tls.port: a shared host ends TLS at its edge")
	case c.Console.Enabled:
		add("mode: managed serves no pages: console.enabled must be false")
	}
	if strings.Contains(strings.Join(c.Templating.Extensions, ","), ExtensionJWT) && (c.Templating.JWT.HS256Secret == "" || c.Templating.JWT.RS256PrivateKey == "") {
		add("mode: managed needs templating.jwt keys (hs256Secret and rs256PrivateKey) with the jwt extension: keys made at start-up differ between instances")
	}
	if !c.Managed.SingleInstance {
		errs = append(errs, c.requireSharedStores(), c.requireSecureLinks())
	}
	return errors.Join(errs...)
}

// requireSharedStores says which shared stores managed mode needs on more than one instance.
func (c *Config) requireSharedStores() error {
	var missing []string
	if c.Storage.Counters.Driver != "redis" {
		missing = append(missing, "storage.counters.driver: redis")
	}
	if c.Storage.Global.Driver != driverPostgres {
		missing = append(missing, "storage.global.driver: postgres")
	}
	if c.Storage.Blobs.Driver != "s3" {
		missing = append(missing, "storage.blobs.driver: s3")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("mode: managed needs shared stores (%s), or managed.singleInstance: true for one instance on in-memory stores", strings.Join(missing, ", "))
}

func (is IssuerConfig) validate(where string, names map[string]bool) error {
	switch {
	case is.Name == "" || is.Issuer == "" || is.Audience == "":
		return fmt.Errorf("%s needs name, issuer and audience", where)
	case names[is.Name]:
		return fmt.Errorf("%s: the name %q is used twice", where, is.Name)
	case (is.JWKSURL == "") == (is.PublicKey == ""):
		return fmt.Errorf("%s needs exactly one of jwksUrl and publicKey", where)
	case time.Duration(is.MaxLifetime) < 0 || time.Duration(is.MaxLifetime) > maxTokenLifetime:
		return fmt.Errorf("%s.maxLifetime must be at most %s", where, maxTokenLifetime)
	}
	names[is.Name] = true
	if is.PublicKey != "" {
		if _, err := auth.ParsePublicKey(is.PublicKey); err != nil {
			return fmt.Errorf("%s.publicKey: %w", where, err)
		}
	}
	for claim, role := range is.RoleMap {
		if role != "admin" && role != "editor" && role != "viewer" {
			return fmt.Errorf("%s.roleMap[%s]: %q is not admin, editor or viewer", where, claim, role)
		}
	}
	return nil
}
