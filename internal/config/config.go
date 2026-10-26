// Package config loads the engine configuration (DESIGN.md §11) from a YAML file and the
// environment. Flags are applied by cmd/shadowmock, so precedence is file < env < flags
// (NFR-6.2). Libraries never read the environment themselves; only Load does, for main.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration written as "1h", "90s" and so on.
type Duration time.Duration

// UnmarshalYAML parses a duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	parsed, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", n.Value, err)
	}
	*d = Duration(parsed)
	return nil
}

// Listen sets the listener addresses.
type Listen struct {
	Mock  string `yaml:"mock"`
	Admin string `yaml:"admin"` // empty: admin shares the mock listener
}

// Admin controls the admin API.
type Admin struct {
	GlobalWrites string `yaml:"globalWrites"` // allow | deny
}

// GlobalStorage selects the global mapping backend.
type GlobalStorage struct {
	Driver string `yaml:"driver"` // fs | memory
	Watch  bool   `yaml:"watch"`
}

// Storage selects backends.
type Storage struct {
	Global GlobalStorage `yaml:"global"`
}

// Journal sizes the request journal.
type Journal struct {
	MaxEntries int `yaml:"maxEntries"` // 0 disables the journal
}

// Respond controls responses.
type Respond struct {
	Gzip          string `yaml:"gzip"` // auto | off
	MatchedHeader bool   `yaml:"matchedHeader"`
}

// Local is the standalone org.
type Local struct {
	IPAllowlist []string `yaml:"ipAllowlist"`
}

// Tenancy configures orgs.
type Tenancy struct {
	Local Local `yaml:"local"`
}

// ClientIP says how to find the client's address behind proxies (DESIGN.md §8.1).
type ClientIP struct {
	Header         string   `yaml:"header"`
	TrustedHops    int      `yaml:"trustedHops"`
	TrustedProxies []string `yaml:"trustedProxies"`
}

// Server configures the HTTP server.
type Server struct {
	ClientIP      ClientIP `yaml:"clientIp"`
	PublicBaseURL string   `yaml:"publicBaseUrl"`
}

// Detect configures key-based session detection.
type Detect struct {
	Headers        []string `yaml:"headers"`
	QueryParams    []string `yaml:"queryParams"`
	Cookies        []string `yaml:"cookies"`
	ScanMetadata   bool     `yaml:"scanMetadata"`
	ScanBody       bool     `yaml:"scanBody"`
	MaxScanBytes   int      `yaml:"maxScanBytes"`
	ContentTypes   []string `yaml:"contentTypes"`
	Decompress     bool     `yaml:"decompress"`
	MinTokenLength int      `yaml:"minTokenLength"`
	Ambiguity      string   `yaml:"ambiguity"`
}

// URLBased configures URL-based shadow mocks.
type URLBased struct {
	IDLength   int      `yaml:"idLength"`
	DefaultTTL Duration `yaml:"defaultTtl"`
	MaxTTL     Duration `yaml:"maxTtl"`
}

// Sessions configures sessions.
type Sessions struct {
	DefaultTTL Duration `yaml:"defaultTtl"`
	MaxTTL     Duration `yaml:"maxTtl"`
	KeyPrefix  string   `yaml:"keyPrefix"`
	Detect     Detect   `yaml:"detect"`
	URLBased   URLBased `yaml:"urlBased"`
}

// Limits caps resource use.
type Limits struct {
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
}

// Log configures logging.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// Config is the whole configuration.
type Config struct {
	Mode     string   `yaml:"mode"` // standalone | server
	Listen   Listen   `yaml:"listen"`
	Admin    Admin    `yaml:"admin"`
	Root     string   `yaml:"root"`
	Storage  Storage  `yaml:"storage"`
	Journal  Journal  `yaml:"journal"`
	Respond  Respond  `yaml:"respond"`
	Tenancy  Tenancy  `yaml:"tenancy"`
	Server   Server   `yaml:"server"`
	Sessions Sessions `yaml:"sessions"`
	Limits   Limits   `yaml:"limits"`
	Log      Log      `yaml:"log"`
}

// Default returns the documented defaults (DESIGN.md §11).
func Default() Config {
	return Config{
		Mode:    "standalone",
		Listen:  Listen{Mock: ":8080"},
		Admin:   Admin{GlobalWrites: "allow"},
		Root:    ".",
		Storage: Storage{Global: GlobalStorage{Driver: "fs", Watch: true}},
		Journal: Journal{MaxEntries: 10000},
		Respond: Respond{Gzip: "auto"},
		Sessions: Sessions{
			DefaultTTL: Duration(time.Hour),
			MaxTTL:     Duration(24 * time.Hour),
			KeyPrefix:  "smk_",
			Detect: Detect{
				Headers: []string{"X-Shadow-Mock-Key"}, QueryParams: []string{"shadow_mock_key"}, Cookies: []string{"shadow_mock_key"},
				ScanMetadata: true, ScanBody: true, MaxScanBytes: 1 << 20, Decompress: true, MinTokenLength: 6, Ambiguity: "first",
				ContentTypes: []string{"text/*", "application/json", "application/*+json", "application/xml", "application/x-www-form-urlencoded"},
			},
			URLBased: URLBased{IDLength: 16, DefaultTTL: Duration(24 * time.Hour), MaxTTL: Duration(30 * 24 * time.Hour)},
		},
		Limits: Limits{MaxBodyBytes: 10 << 20},
		Log:    Log{Level: "info", Format: "text"},
	}
}

// Load reads the YAML file at path (if not empty) over the defaults, then applies
// SHADOWMOCK_* environment variables.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the operator chooses the config file
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) { // an empty file is valid
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg, os.LookupEnv); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyEnv overrides settings from the environment.
func applyEnv(cfg *Config, lookup func(string) (string, bool)) error {
	strs := map[string]*string{
		"SHADOWMOCK_MODE":            &cfg.Mode,
		"SHADOWMOCK_LISTEN_MOCK":     &cfg.Listen.Mock,
		"SHADOWMOCK_LISTEN_ADMIN":    &cfg.Listen.Admin,
		"SHADOWMOCK_ROOT":            &cfg.Root,
		"SHADOWMOCK_LOG_LEVEL":       &cfg.Log.Level,
		"SHADOWMOCK_LOG_FORMAT":      &cfg.Log.Format,
		"SHADOWMOCK_PUBLIC_BASE_URL": &cfg.Server.PublicBaseURL,
	}
	for k, dst := range strs {
		if v, ok := lookup(k); ok {
			*dst = v
		}
	}
	if v, ok := lookup("SHADOWMOCK_JOURNAL_MAX_ENTRIES"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("SHADOWMOCK_JOURNAL_MAX_ENTRIES: %w", err)
		}
		cfg.Journal.MaxEntries = n
	}
	return nil
}

// Validate checks values that would otherwise fail later.
func (c *Config) Validate() error {
	switch {
	case c.Mode != "standalone" && c.Mode != "server":
		return fmt.Errorf("mode must be standalone or server, not %q", c.Mode)
	case c.Admin.GlobalWrites != "allow" && c.Admin.GlobalWrites != "deny":
		return fmt.Errorf("admin.globalWrites must be allow or deny, not %q", c.Admin.GlobalWrites)
	case c.Storage.Global.Driver != "fs" && c.Storage.Global.Driver != "memory":
		return fmt.Errorf("storage.global.driver must be fs or memory, not %q", c.Storage.Global.Driver)
	case c.Sessions.Detect.Ambiguity != "first" && c.Sessions.Detect.Ambiguity != "reject" && c.Sessions.Detect.Ambiguity != "global":
		return fmt.Errorf("sessions.detect.ambiguity must be first, reject or global, not %q", c.Sessions.Detect.Ambiguity)
	case c.Sessions.URLBased.IDLength < 16:
		return errors.New("sessions.urlBased.idLength must be at least 16 (80 bits, NFR-5.1)")
	}
	return nil
}
