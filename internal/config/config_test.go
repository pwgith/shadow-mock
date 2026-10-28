package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/config"
)

func env(kv ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "shadowmock.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultIsValid(t *testing.T) {
	t.Parallel()
	c := config.Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("the defaults must be valid: %v", err)
	}
	if c.Listen.Mock != ":8080" || c.AdminPrefix != "/__admin" || c.Sessions.DefaultTTL != config.Duration(time.Hour) ||
		c.Sessions.URLBased.MaxTTL != config.Duration(30*24*time.Hour) || c.Sessions.KeyPrefix != "smk_" {
		t.Errorf("defaults do not match DESIGN.md §11: %+v", c)
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	t.Run("a file is merged over the defaults", func(t *testing.T) {
		t.Parallel()
		c, err := config.Load(writeFile(t, "listen: { mock: ':9000' }\nsessions: { defaultTtl: 30m }\nstorage: { journal: { hot: { maxEntries: 5 } } }\n"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.Listen.Mock != ":9000" || c.Sessions.DefaultTTL != config.Duration(30*time.Minute) || c.Storage.Journal.Hot.MaxEntries != 5 {
			t.Errorf("merged = %+v", c)
		}
		if c.Sessions.MaxTTL != config.Duration(24*time.Hour) || c.AdminPrefix != "/__admin" {
			t.Errorf("settings the file does not mention must keep their defaults: %+v", c)
		}
	})
	t.Run("an empty file is valid", func(t *testing.T) {
		t.Parallel()
		if _, err := config.Load(writeFile(t, ""), nil); err != nil {
			t.Error(err)
		}
	})
	t.Run("no file uses the defaults", func(t *testing.T) {
		t.Parallel()
		if _, err := config.Load("", nil); err != nil {
			t.Error(err)
		}
	})
	t.Run("unknown keys fail, naming the key", func(t *testing.T) {
		t.Parallel()
		_, err := config.Load(writeFile(t, "sesions: { defaultTtl: 1h }\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "sesions") {
			t.Errorf("error = %v, want one naming sesions", err)
		}
	})
	t.Run("an unreadable file", func(t *testing.T) {
		t.Parallel()
		if _, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"), nil); err == nil {
			t.Error("a missing file should fail")
		}
	})
	t.Run("a bad duration", func(t *testing.T) {
		t.Parallel()
		_, err := config.Load(writeFile(t, "sessions: { defaultTtl: soon }\n"), nil)
		if err == nil || !strings.Contains(err.Error(), "duration") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("invalid values fail validation", func(t *testing.T) {
		t.Parallel()
		if _, err := config.Load(writeFile(t, "mode: cluster\n"), nil); err == nil {
			t.Error("mode: cluster should fail")
		}
	})
}

func TestEnvironment(t *testing.T) {
	t.Parallel()
	c, err := config.Load("", env(
		"SHADOWMOCK_LISTEN_MOCK", ":7001", "SHADOWMOCK_ROOT", "/data", "SHADOWMOCK_LOG_LEVEL", "debug",
		"SHADOWMOCK_PUBLIC_BASE_URL", "https://mock.example", "SHADOWMOCK_JOURNAL_MAX_ENTRIES", "42",
		"SHADOWMOCK_LISTEN_ADMIN", ":7002", "SHADOWMOCK_LOG_FORMAT", "json", "SHADOWMOCK_MODE", "standalone"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Mock != ":7001" || c.Listen.Admin != ":7002" || c.Root != "/data" || c.Log.Level != "debug" || c.Log.Format != "json" ||
		c.Server.PublicBaseURL != "https://mock.example" || c.Storage.Journal.Hot.MaxEntries != 42 || c.Mode != "standalone" {
		t.Errorf("config = %+v", c)
	}
	if _, err := config.Load("", env("SHADOWMOCK_MODE", "server")); err == nil {
		t.Error("server mode needs admin tokens, which the environment cannot give")
	}
	if _, err := config.Load("", env("SHADOWMOCK_JOURNAL_MAX_ENTRIES", "many")); err == nil {
		t.Error("a non-numeric journal size should fail")
	}
	// An empty variable does not override anything.
	if c, err := config.Load("", env("SHADOWMOCK_LISTEN_MOCK", "")); err != nil || c.Listen.Mock != ":8080" {
		t.Errorf("empty variable: %v %q", err, c.Listen.Mock)
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	file := writeFile(t, "listen: { mock: ':7001' }\n")
	tests := []struct {
		name string
		args []string
		env  func(string) (string, bool)
		want string
	}{
		{"file only", []string{"--config", file}, nil, ":7001"},
		{"the environment beats the file", []string{"--config", file}, env("SHADOWMOCK_LISTEN_MOCK", ":7002"), ":7002"},
		{"a flag beats the environment", []string{"--config", file, "--listen-mock", ":7003"}, env("SHADOWMOCK_LISTEN_MOCK", ":7002"), ":7003"},
		{"--port is a shorthand", []string{"--port", "7004"}, nil, ":7004"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _, err := config.Resolve(tt.args, tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if c.Listen.Mock != tt.want {
				t.Errorf("listen.mock = %q, want %q", c.Listen.Mock, tt.want)
			}
		})
	}
}

func TestResolveFlags(t *testing.T) {
	t.Parallel()
	c, f, err := config.Resolve([]string{"--listen-admin", ":9", "--root-dir", "/x", "--verbose"}, nil)
	if err != nil || f.ShowVersion {
		t.Fatalf("%v %+v", err, f)
	}
	if c.Listen.Admin != ":9" || c.Root != "/x" || c.Log.Level != "debug" {
		t.Errorf("config = %+v", c)
	}
	if _, f, err := config.Resolve([]string{"--version"}, nil); err != nil || !f.ShowVersion {
		t.Errorf("--version: %v %+v", err, f)
	}
	if _, _, err := config.Resolve([]string{"--nope"}, nil); err == nil {
		t.Error("an unknown flag should fail")
	}
	if _, _, err := config.Resolve([]string{"--config", filepath.Join(t.TempDir(), "x.yaml")}, nil); err == nil {
		t.Error("a missing config file should fail")
	}
	if _, _, err := config.Resolve([]string{"--listen-mock", ""}, env("SHADOWMOCK_MODE", "other")); err == nil {
		t.Error("an invalid environment value should fail")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*config.Config)
		want string
	}{
		{"mode", func(c *config.Config) { c.Mode = "x" }, "mode must be one of standalone, server"},
		{"global writes", func(c *config.Config) { c.Admin.GlobalWrites = "x" }, "admin.globalWrites"},
		{"global storage", func(c *config.Config) { c.Storage.Global.Driver = "postgres" }, "storage.global.driver"},
		{"session storage", func(c *config.Config) { c.Storage.Sessions.Driver = "redis" }, "storage.sessions.driver"},
		{"hot journal", func(c *config.Config) { c.Storage.Journal.Hot.Driver = "redis" }, "storage.journal.hot.driver"},
		{"warm journal", func(c *config.Config) { c.Storage.Journal.Warm.Driver = "postgres" }, "storage.journal.warm.driver"},
		{"counters", func(c *config.Config) { c.Storage.Counters.Driver = "redis" }, "storage.counters.driver"},
		{"blobs", func(c *config.Config) { c.Storage.Blobs.Driver = "s3" }, "storage.blobs.driver"},
		{"auth", func(c *config.Config) { c.Auth.Driver = "jwt" }, "auth.driver"},
		{"tenancy", func(c *config.Config) { c.Tenancy.Directory = "postgres" }, "tenancy.directory"},
		{"gzip", func(c *config.Config) { c.Respond.Gzip = "always" }, "respond.gzip"},
		{"ambiguity", func(c *config.Config) { c.Sessions.Detect.Ambiguity = "sometimes" }, "ambiguity must be one of first, reject, global"},
		{"id length", func(c *config.Config) { c.Sessions.URLBased.IDLength = 8 }, "at least 16"},
		{"admin prefix without a slash", func(c *config.Config) { c.AdminPrefix = "admin" }, "adminPrefix"},
		{"admin prefix with a trailing slash", func(c *config.Config) { c.AdminPrefix = "/admin/" }, "adminPrefix"},
		{"path prefix", func(c *config.Config) { c.Sessions.URLBased.PathPrefix = "/mock" }, "pathPrefix"},
		{"negative journal", func(c *config.Config) { c.Storage.Journal.Hot.MaxEntries = -1 }, "must not be negative"},
		{"negative session limit", func(c *config.Config) { c.Limits.MaxSessions = -1 }, "limits must not be negative"},
		{"negative mapping limit", func(c *config.Config) { c.Limits.MaxMappingsPerSession = -1 }, "limits must not be negative"},
		{"negative delay limit", func(c *config.Config) { c.Limits.MaxDelay = config.Duration(-time.Second) }, "limits must not be negative"},
		{"negative rate", func(c *config.Config) { c.Limits.RequestsPerSecond = -0.5 }, "limits must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := config.Default()
			tt.edit(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()
	c := config.Default()
	if err := config.Merge(&c, []byte("limits: { maxBodyBytes: 1024 }")); err != nil || c.Limits.MaxBodyBytes != 1024 {
		t.Errorf("Merge = %v, %d", err, c.Limits.MaxBodyBytes)
	}
	if err := config.Merge(&c, []byte("limits: [1")); err == nil {
		t.Error("malformed YAML should fail")
	}
}
