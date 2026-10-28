package config_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/config"
)

const secret = "0123456789abcdef-secret"

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func load(t *testing.T, yaml string, env map[string]string) (config.Config, error) {
	t.Helper()
	cfg := config.Default()
	if err := config.Merge(&cfg, []byte(yaml)); err != nil {
		return cfg, err
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if err := config.ResolveSecrets(&cfg, lookup); err != nil {
		return cfg, err
	}
	err := cfg.Validate()
	return cfg, err
}

func TestAdminTokensAreAcceptedInTheirThreeForms(t *testing.T) {
	t.Parallel()
	cfg, err := load(t, `
auth:
  driver: token
  tokens:
    - { name: plain, role: admin, token: "`+secret+`" }
    - { name: fromenv, role: editor, tokenEnv: CI_TOKEN }
    - { name: hashed, role: viewer, tokenSha256: "`+hash("another-secret-value")+`" }
`, map[string]string{"CI_TOKEN": "ci-secret-0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"plain": hash(secret), "fromenv": hash("ci-secret-0123456789"), "hashed": hash("another-secret-value")}
	for _, tok := range cfg.Auth.Tokens {
		if got := hex.EncodeToString(tok.Hash[:]); got != want[tok.Name] {
			t.Errorf("token %s hash = %s, want %s", tok.Name, got, want[tok.Name])
		}
	}
}

func TestAdminTokenMistakesAreRefusedWithAClearMessage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, yaml, want string }{
		{"no tokens", "auth: { driver: token }", "at least one entry"},
		{"no admin token", "auth: { driver: token, tokens: [ { name: a, role: editor, token: \"" + secret + "\" } ] }", "admin role"},
		{"a short token", "auth: { driver: token, tokens: [ { name: a, role: admin, token: short } ] }", "at least 16"},
		{"an unknown role", "auth: { driver: token, tokens: [ { name: a, role: root, token: \"" + secret + "\" } ] }", "viewer, editor, admin"},
		{"a bad name", "auth: { driver: token, tokens: [ { name: \"a b\", role: admin, token: \"" + secret + "\" } ] }", "name"},
		{"a name used twice", "auth: { driver: token, tokens: [ { name: a, role: admin, token: \"" + secret + "\" }, { name: a, role: viewer, token: \"" + secret + "x\" } ] }", "used twice"},
		{"two forms at once", "auth: { driver: token, tokens: [ { name: a, role: admin, token: \"" + secret + "\", tokenSha256: \"" + hash("x") + "\" } ] }", "exactly one"},
		{"no form", "auth: { driver: token, tokens: [ { name: a, role: admin } ] }", "exactly one"},
		{"a bad hash", "auth: { driver: token, tokens: [ { name: a, role: admin, tokenSha256: zz } ] }", "64 hexadecimal"},
		{"tokens without the driver", "auth: { tokens: [ { name: a, role: admin, token: \"" + secret + "\" } ] }", "auth.driver is not token"},
		{"server mode without tokens", "mode: server", "mode: server needs auth.driver: token"},
	} {
		_, err := load(t, tt.yaml, nil)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: got %v, want an error mentioning %q", tt.name, err, tt.want)
		}
	}
}

func TestAnEnvironmentVariableThatIsNotSetIsAnError(t *testing.T) {
	t.Parallel()
	_, err := load(t, "auth: { driver: token, tokens: [ { name: a, role: admin, tokenEnv: MISSING_TOKEN } ] }", nil)
	if err == nil || !strings.Contains(err.Error(), "MISSING_TOKEN is not set") {
		t.Errorf("got %v, want a message naming the variable", err)
	}
}

func TestServerModeWithAnAdminTokenIsAccepted(t *testing.T) {
	t.Parallel()
	if _, err := load(t, "mode: server\nauth: { driver: token, tokens: [ { name: ops, role: admin, token: \""+secret+"\" } ] }", nil); err != nil {
		t.Fatal(err)
	}
}

func TestWarningsNameSettingsThatDoNothingOrLeaveTheServerExposed(t *testing.T) {
	t.Parallel()
	cfg, err := load(t, "storage: { blobs: { driver: memory, dir: ./x } }\nauth: { driver: token, tokens: [ { name: ops, role: admin, token: \""+secret+"\" } ] }", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{"storage.blobs has no effect", "keeps its secret in the configuration", "admin.allowedHosts is empty"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning mentioning %q in:\n%s", want, joined)
		}
	}
	quiet := config.Default()
	if w := quiet.Warnings(); len(w) != 0 {
		t.Errorf("the defaults should warn about nothing, got %v", w)
	}
}
