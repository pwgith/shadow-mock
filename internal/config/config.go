// Package config loads the engine configuration (DESIGN.md §11). Precedence is file, then
// environment, then flags (NFR-6.2). The environment is read only through the lookup function
// the caller passes in, so libraries never read it themselves.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
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
	Admin string `yaml:"admin"` // empty: admin shares the mock listener under AdminPrefix
}

// Admin controls the admin API.
type Admin struct {
	GlobalWrites string `yaml:"globalWrites"` // allow | deny
}

// Driver selects a backend.
type Driver struct {
	Driver string `yaml:"driver"`
}

// GlobalStorage selects the global mapping backend.
type GlobalStorage struct {
	Driver string `yaml:"driver"` // fs | memory
	Watch  bool   `yaml:"watch"`
}

// HotJournal sizes the hot journal (DESIGN.md §5.2).
type HotJournal struct {
	Driver       string `yaml:"driver"`     // memory (standalone) | redis (SaaS)
	MaxEntries   int    `yaml:"maxEntries"` // 0 disables the journal
	MaxBodyBytes int    `yaml:"maxBodyBytes"`
}

// Journal configures the journal tiers.
type Journal struct {
	Hot  HotJournal `yaml:"hot"`
	Warm Driver     `yaml:"warm"` // none (standalone) | postgres (SaaS)
}

// Blobs configures the blob store.
type Blobs struct {
	Driver string `yaml:"driver"` // fs | memory | s3
	Dir    string `yaml:"dir"`
}

// Storage selects backends per concern.
type Storage struct {
	Global   GlobalStorage `yaml:"global"`
	Sessions Driver        `yaml:"sessions"` // memory | fs (persist sessions under <root>/sessions)
	Journal  Journal       `yaml:"journal"`
	Counters Driver        `yaml:"counters"` // memory | redis
	Blobs    Blobs         `yaml:"blobs"`
}

// Respond controls responses.
type Respond struct {
	Gzip          string `yaml:"gzip"` // auto | off
	MatchedHeader bool   `yaml:"matchedHeader"`
}

// Auth selects the authenticator.
type Auth struct {
	Driver  string           `yaml:"driver"` // noop | jwt (SaaS)
	Issuers []map[string]any `yaml:"issuers"`
}

// Local is the standalone org.
type Local struct {
	IPAllowlist []string `yaml:"ipAllowlist"`
}

// Tenancy configures orgs.
type Tenancy struct {
	Directory         string `yaml:"directory"` // static | postgres (SaaS)
	RequireOrgSegment bool   `yaml:"requireOrgSegment"`
	Local             Local  `yaml:"local"`
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
	PathPrefix string   `yaml:"pathPrefix"`
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

// Templating configures response templating (P3).
type Templating struct {
	Global bool `yaml:"global"`
}

// Log configures logging.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// Config is the whole configuration.
type Config struct {
	Mode        string     `yaml:"mode"` // standalone | server
	Listen      Listen     `yaml:"listen"`
	AdminPrefix string     `yaml:"adminPrefix"`
	Admin       Admin      `yaml:"admin"`
	Root        string     `yaml:"root"`
	Storage     Storage    `yaml:"storage"`
	Respond     Respond    `yaml:"respond"`
	Auth        Auth       `yaml:"auth"`
	Tenancy     Tenancy    `yaml:"tenancy"`
	Server      Server     `yaml:"server"`
	Sessions    Sessions   `yaml:"sessions"`
	Limits      Limits     `yaml:"limits"`
	Templating  Templating `yaml:"templating"`
	Log         Log        `yaml:"log"`
}

// Default returns the documented defaults (DESIGN.md §11).
func Default() Config {
	return Config{
		Mode:        "standalone",
		Listen:      Listen{Mock: ":8080"},
		AdminPrefix: "/__admin",
		Admin:       Admin{GlobalWrites: "allow"},
		Root:        ".",
		Storage: Storage{
			Global:   GlobalStorage{Driver: "fs", Watch: true},
			Sessions: Driver{Driver: "memory"},
			Journal:  Journal{Hot: HotJournal{Driver: "memory", MaxEntries: 10000, MaxBodyBytes: 65536}, Warm: Driver{Driver: "none"}},
			Counters: Driver{Driver: "memory"},
			Blobs:    Blobs{Driver: "fs", Dir: "./blobs"},
		},
		Respond: Respond{Gzip: "auto"},
		Auth:    Auth{Driver: "noop"},
		Tenancy: Tenancy{Directory: "static"},
		Sessions: Sessions{
			DefaultTTL: Duration(time.Hour),
			MaxTTL:     Duration(24 * time.Hour),
			KeyPrefix:  "smk_",
			Detect: Detect{
				Headers: []string{"X-Shadow-Mock-Key"}, QueryParams: []string{"shadow_mock_key"}, Cookies: []string{"shadow_mock_key"},
				ScanMetadata: true, ScanBody: true, MaxScanBytes: 1 << 20, Decompress: true, MinTokenLength: 6, Ambiguity: "first",
				ContentTypes: []string{"text/*", "application/json", "application/*+json", "application/xml", "application/x-www-form-urlencoded"},
			},
			URLBased: URLBased{PathPrefix: "/d", IDLength: 16, DefaultTTL: Duration(24 * time.Hour), MaxTTL: Duration(30 * 24 * time.Hour)},
		},
		Limits: Limits{MaxBodyBytes: 10 << 20},
		Log:    Log{Level: "info", Format: "text"},
	}
}

// Flags are the command-line options that are not configuration.
type Flags struct {
	ShowVersion bool
}

// Resolve builds the configuration from command-line arguments, the environment (through
// lookup) and the file named by --config, with flags winning over the environment and the
// environment over the file (NFR-6.2).
func Resolve(args []string, lookup func(string) (string, bool)) (Config, Flags, error) {
	fl := flag.NewFlagSet("shadowmock", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "YAML configuration file")
	listenMock := fl.String("listen-mock", "", "mock listener address, such as :8080 (overrides listen.mock)")
	listenAdmin := fl.String("listen-admin", "", "separate admin listener address (overrides listen.admin)")
	port := fl.Int("port", 0, "mock listener port; shorthand for --listen-mock :<port>")
	root := fl.String("root-dir", "", "directory holding mappings/ and __files/ (overrides root)")
	verbose := fl.Bool("verbose", false, "log at debug level")
	var f Flags
	fl.BoolVar(&f.ShowVersion, "version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return Config{}, f, fmt.Errorf("parse flags: %w", err)
	}
	if f.ShowVersion {
		return Config{}, f, nil
	}
	cfg, err := Load(*cfgPath, lookup)
	if err != nil {
		return Config{}, f, err
	}
	if *port != 0 {
		cfg.Listen.Mock = ":" + strconv.Itoa(*port)
	}
	for dst, v := range map[*string]string{&cfg.Listen.Mock: *listenMock, &cfg.Listen.Admin: *listenAdmin, &cfg.Root: *root} {
		if v != "" {
			*dst = v
		}
	}
	if *verbose {
		cfg.Log.Level = "debug"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, f, err
	}
	return cfg, f, nil
}

// Load reads the YAML file at path (if not empty) over the defaults, then applies
// SHADOWMOCK_* variables found through lookup. Unknown keys are errors.
func Load(path string, lookup func(string) (string, bool)) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the operator chooses the config file
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := Merge(&cfg, data); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if lookup != nil {
		if err := applyEnv(&cfg, lookup); err != nil {
			return Config{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Merge decodes YAML over cfg. Keys not present keep their current values; unknown keys are
// rejected so typos fail fast.
func Merge(cfg *Config, data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) { // an empty document is valid
		return fmt.Errorf("yaml: %w", err)
	}
	return nil
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
		if v, ok := lookup(k); ok && v != "" {
			*dst = v
		}
	}
	if v, ok := lookup("SHADOWMOCK_JOURNAL_MAX_ENTRIES"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("SHADOWMOCK_JOURNAL_MAX_ENTRIES: %w", err)
		}
		cfg.Storage.Journal.Hot.MaxEntries = n
	}
	return nil
}

func oneOf(name, v string, allowed ...string) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s, not %q", name, strings.Join(allowed, ", "), v)
}

// Validate checks values that would otherwise fail later. Backends that belong to SaaS
// (postgres, redis, s3, jwt) are rejected until they are built (DESIGN.md §15, P5).
func (c *Config) Validate() error {
	checks := []error{
		oneOf("mode", c.Mode, "standalone", "server"),
		oneOf("admin.globalWrites", c.Admin.GlobalWrites, "allow", "deny"),
		oneOf("storage.global.driver", c.Storage.Global.Driver, "fs", "memory"),
		oneOf("storage.sessions.driver", c.Storage.Sessions.Driver, "memory", "fs"),
		oneOf("storage.journal.hot.driver", c.Storage.Journal.Hot.Driver, "memory"),
		oneOf("storage.journal.warm.driver", c.Storage.Journal.Warm.Driver, "none"),
		oneOf("storage.counters.driver", c.Storage.Counters.Driver, "memory"),
		oneOf("storage.blobs.driver", c.Storage.Blobs.Driver, "fs", "memory"),
		oneOf("auth.driver", c.Auth.Driver, "noop"),
		oneOf("tenancy.directory", c.Tenancy.Directory, "static"),
		oneOf("respond.gzip", c.Respond.Gzip, "auto", "off"),
		oneOf("sessions.detect.ambiguity", c.Sessions.Detect.Ambiguity, "first", "reject", "global"),
	}
	if err := errors.Join(checks...); err != nil {
		return err
	}
	switch {
	case c.Sessions.URLBased.IDLength < 16:
		return errors.New("sessions.urlBased.idLength must be at least 16 (80 bits, NFR-5.1)")
	case !strings.HasPrefix(c.AdminPrefix, "/") || strings.HasSuffix(c.AdminPrefix, "/"):
		return fmt.Errorf("adminPrefix must start with / and not end with one, not %q", c.AdminPrefix)
	case c.Sessions.URLBased.PathPrefix != "/d":
		return errors.New("sessions.urlBased.pathPrefix other than /d is not supported yet")
	case c.Storage.Journal.Hot.MaxEntries < 0:
		return errors.New("storage.journal.hot.maxEntries must not be negative")
	}
	return nil
}
