package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The extensions that can be switched on (FR-10.16). Each stands in for one of WireMock's separately
// published extensions: jwt for wiremock-jwt-extension, faker for wiremock-faker-extension and graphql for
// wiremock-graphql-extension.
const (
	ExtensionJWT     = "jwt"
	ExtensionFaker   = "faker"
	ExtensionGraphQL = "graphql"
)

// JWT holds the keys of the jwt template helper. Whatever is empty is generated at start-up.
type JWT struct {
	HS256Secret      string `yaml:"hs256Secret"`
	RS256PublicKeyID string `yaml:"rs256PublicKeyId"`
	RS256PublicKey   string `yaml:"rs256PublicKey"`
	RS256PrivateKey  string `yaml:"rs256PrivateKey"`
}

// Matching configures request matching.
type Matching struct {
	// Extensions lists the matcher extensions to switch on: graphql.
	Extensions []string `yaml:"extensions"`
}

// javaExtensions maps the Java classes WireMock is told about with --extensions to the extension that
// replaces them.
func javaExtensions() map[string]string {
	return map[string]string{
		"org.wiremock.extension.jwt.JwtExtensionFactory": ExtensionJWT,
		"org.wiremock.RandomExtension":                   ExtensionFaker,
		"io.github.nilwurtz.GraphqlBodyMatcher":          ExtensionGraphQL,
	}
}

// enable switches an extension on in the section it belongs to.
func (c *Config) enable(ext string) {
	list := &c.Templating.Extensions
	if ext == ExtensionGraphQL {
		list = &c.Matching.Extensions
	}
	if !slices.Contains(*list, ext) {
		*list = append(*list, ext)
	}
}

func checkExtensions(key string, got []string, allowed ...string) error {
	for _, e := range got {
		if !slices.Contains(allowed, e) {
			return fmt.Errorf("%s: %q is not an extension Shadow Mock has; the choices are %s", key, e, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func (c *Config) validateExtensions() error {
	if err := checkExtensions("templating.extensions", c.Templating.Extensions, ExtensionJWT, ExtensionFaker); err != nil {
		return err
	}
	if err := checkExtensions("matching.extensions", c.Matching.Extensions, ExtensionGraphQL); err != nil {
		return err
	}
	j := c.Templating.JWT
	if (j.RS256PublicKey != "" || j.RS256PublicKeyID != "" || j.HS256Secret != "") && !slices.Contains(c.Templating.Extensions, ExtensionJWT) {
		return errors.New("templating.jwt needs jwt in templating.extensions")
	}
	if j.RS256PublicKey != "" && j.RS256PrivateKey == "" {
		return errors.New("templating.jwt.rs256PublicKey needs templating.jwt.rs256PrivateKey")
	}
	return nil
}
