package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// minTokenLength is the shortest secret accepted. A token is a password for the admin API, so it
// should come from a random source; 16 characters is the least that is not guessable.
const minTokenLength = 16

var tokenName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ResolveSecrets reads the secret of each token that names an environment variable.
func ResolveSecrets(c *Config, lookup func(string) (string, bool)) error {
	if env := c.Auth.Basic.PasswordEnv; env != "" && c.Auth.Basic.Password == "" {
		v, ok := lookup(env)
		if !ok || v == "" {
			return fmt.Errorf("auth.basic.passwordEnv: the environment variable %s is not set", env)
		}
		c.Auth.Basic.Password = v
	}
	for i := range c.Auth.Tokens {
		t := &c.Auth.Tokens[i]
		if t.TokenEnv == "" {
			continue
		}
		v, ok := lookup(t.TokenEnv)
		if !ok || v == "" {
			return fmt.Errorf("auth.tokens[%d] (%s): the environment variable %s is not set", i, t.Name, t.TokenEnv)
		}
		t.secret = v
	}
	return nil
}

// validateAuth checks the admin tokens and works out their hashes.
func (c *Config) validateAuth() error {
	if c.Mode == "managed" {
		return nil // validateManaged checks its credentials
	}
	switch {
	case c.Mode == "server" && c.Auth.Driver != "token":
		return errors.New("mode: server needs auth.driver: token, with at least one admin token, so the admin API is never open (see docs/guide/12-shared-server.md)")
	case c.Auth.Driver == "basic":
		switch {
		case len(c.Auth.Tokens) > 0:
			return errors.New("auth.tokens are given but auth.driver is basic; use one or the other")
		case c.Auth.Basic.Username == "" || (c.Auth.Basic.Password == "" && c.Auth.Basic.PasswordEnv == ""):
			return errors.New("auth.driver: basic needs auth.basic.username and a password (auth.basic.password, or passwordEnv to name an environment variable)")
		}
		return nil
	case c.Auth.Driver != "token":
		if len(c.Auth.Tokens) > 0 {
			return errors.New("auth.tokens are given but auth.driver is not token, so the admin API would be open; set auth.driver: token")
		}
		return nil
	case len(c.Auth.Tokens) == 0:
		return errors.New("auth.driver: token needs at least one entry in auth.tokens")
	}
	return c.validateTokens()
}

// validateTokens checks each admin token's name, role and secret, and that one of them is an admin.
func (c *Config) validateTokens() error {
	seen := map[string]bool{}
	admins := 0
	var errs []error
	for i := range c.Auth.Tokens {
		t := &c.Auth.Tokens[i]
		where := fmt.Sprintf("auth.tokens[%d]", i)
		if !tokenName.MatchString(t.Name) {
			errs = append(errs, fmt.Errorf("%s: name %q must be 1 to 64 letters, digits, dots, dashes or underscores", where, t.Name))
		} else if seen[t.Name] {
			errs = append(errs, fmt.Errorf("%s: the name %q is used twice", where, t.Name))
		}
		seen[t.Name] = true
		if err := oneOf(where+".role", t.Role, "viewer", "editor", "admin"); err != nil {
			errs = append(errs, err)
		}
		if t.Role == "admin" {
			admins++
		}
		hash, err := t.secretHash(where)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		t.Hash = hash
	}
	if admins == 0 && len(errs) == 0 {
		errs = append(errs, errors.New("auth.tokens needs at least one token with the admin role, or nobody can change the global mappings or stop the server"))
	}
	return errors.Join(errs...)
}

// secretHash returns the SHA-256 of the token's secret, from whichever of the three forms was given.
func (t *AdminToken) secretHash(where string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	given := 0
	for _, s := range []string{t.Token, t.TokenEnv, t.TokenSHA256} {
		if s != "" {
			given++
		}
	}
	if given != 1 {
		return zero, fmt.Errorf("%s (%s): give exactly one of token, tokenEnv and tokenSha256", where, t.Name)
	}
	if t.TokenSHA256 != "" {
		raw, err := hex.DecodeString(strings.TrimSpace(t.TokenSHA256))
		if err != nil || len(raw) != sha256.Size {
			return zero, fmt.Errorf("%s (%s): tokenSha256 must be 64 hexadecimal characters", where, t.Name)
		}
		return [sha256.Size]byte(raw), nil
	}
	secret := t.Token
	if t.TokenEnv != "" {
		secret = t.secret
	}
	if t.TokenEnv != "" && secret == "" {
		return zero, fmt.Errorf("%s (%s): the environment variable %s was not read; load the configuration with Load", where, t.Name, t.TokenEnv)
	}
	if len(secret) < minTokenLength {
		return zero, fmt.Errorf("%s (%s): a token must be at least %d characters; make one with: openssl rand -base64 32", where, t.Name, minTokenLength)
	}
	return sha256.Sum256([]byte(secret)), nil
}

// Warnings lists settings that are accepted but do nothing yet, or that leave the server less
// protected than it could be. The command logs each at startup.
func (c *Config) Warnings() []string {
	var out []string
	d := Default()
	if c.Storage.Blobs != d.Storage.Blobs {
		out = append(out, "storage.blobs has no effect yet: it is reserved for the hosted service")
	}
	if c.Auth.Driver == "basic" {
		out = append(out, "auth.driver: basic sends the password with every admin call; keep the admin API on a network you trust, or behind TLS")
	}
	if c.Auth.Driver == "token" {
		for _, t := range c.Auth.Tokens {
			if t.Token != "" {
				out = append(out, fmt.Sprintf("auth.tokens: %q keeps its secret in the configuration; prefer tokenEnv or tokenSha256", t.Name))
			}
		}
		if len(c.Admin.AllowedHosts) == 0 {
			out = append(out, "admin.allowedHosts is empty, so any Host header reaches the admin API; list the names clients use to guard against DNS rebinding")
		}
	}
	return out
}
