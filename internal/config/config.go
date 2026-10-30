// Package config loads the engine configuration (DESIGN.md §11). Precedence is file, then
// environment, then flags (NFR-6.2). The environment is read only through the lookup function
// the caller passes in, so libraries never read it themselves.
package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
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
	// BindAddress is the address to listen on when listen.mock and listen.admin name only a port
	// (WireMock's --bind-address).
	BindAddress string `yaml:"bindAddress"`
}

// Admin controls the admin API.
type Admin struct {
	GlobalWrites string `yaml:"globalWrites"` // allow | deny
	// AllowedHosts, when not empty, limits the Host header the admin API and the metrics answer to,
	// so a web page cannot reach them through DNS rebinding. Names only, without a port.
	AllowedHosts []string `yaml:"allowedHosts"`
	// AllowedOrigins lists web pages (scheme://host[:port]) that may make changes through a browser,
	// such as a management page served from another address. Empty allows none.
	AllowedOrigins []string `yaml:"allowedOrigins"`
}

// Driver selects a backend.
type Driver struct {
	Driver string `yaml:"driver"`
}

// GlobalStorage selects the global mapping backend.
type GlobalStorage struct {
	Driver string `yaml:"driver"` // fs | memory
	Watch  bool   `yaml:"watch"`
	// FilenameTemplate names the files mappings are written to, as a template over id, name,
	// method and url (WireMock's --filename-template). Empty means the name, or the method and URL,
	// then the id.
	FilenameTemplate string `yaml:"filenameTemplate"`
}

// HotJournal sizes the hot journal (DESIGN.md §5.2).
type HotJournal struct {
	Driver       string `yaml:"driver"`       // memory (standalone) | redis (SaaS)
	MaxEntries   int    `yaml:"maxEntries"`   // 0 disables the journal
	MaxBodyBytes int    `yaml:"maxBodyBytes"` // 0 keeps request and response bodies whole
	MaxBytes     int64  `yaml:"maxBytes"`     // memory budget for the whole journal; the oldest entries go first
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
	Driver string `yaml:"driver"` // noop | token | basic | jwt (SaaS)
	// Basic is the one username and password of auth.driver: basic (WireMock's --admin-api-basic-auth).
	Basic   BasicAuth        `yaml:"basic"`
	Issuers []map[string]any `yaml:"issuers"`
	// Tokens are the admin tokens when driver is token.
	Tokens []AdminToken `yaml:"tokens"`
}

// BasicAuth is a username and password for the admin API.
type BasicAuth struct {
	Username    string `yaml:"username"`
	Password    string `yaml:"password,omitempty"`
	PasswordEnv string `yaml:"passwordEnv,omitempty"` // the name of an environment variable holding the password
}

// AdminToken is one admin token. Give exactly one of Token, TokenEnv or TokenSHA256; the last keeps
// the secret out of the file, holding only its hash.
type AdminToken struct {
	Name        string `yaml:"name"`
	Role        string `yaml:"role"`                  // viewer | editor | admin
	Token       string `yaml:"token,omitempty"`       // the secret, at least 16 characters
	TokenEnv    string `yaml:"tokenEnv,omitempty"`    // the name of an environment variable holding it
	TokenSHA256 string `yaml:"tokenSha256,omitempty"` // the hex SHA-256 of it

	// Hash is the SHA-256 of the secret, worked out by Validate.
	Hash   [sha256.Size]byte `yaml:"-"`
	secret string            // the value of TokenEnv, read by ResolveSecrets
}

// Local is the standalone org.
type Local struct {
	IPAllowlist []string `yaml:"ipAllowlist"`
	// AllowlistAdmin applies the allowlist to the admin API as well as to mock traffic.
	AllowlistAdmin bool `yaml:"allowlistAdmin"`
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
	// StubCORS adds the Access-Control-* headers to mock traffic and answers CORS preflight
	// requests, as WireMock does with --enable-stub-cors (FR-10.11).
	StubCORS bool `yaml:"stubCors"`
	// ServeFiles serves the files in __files over HTTP: at /__files/<path>, and at /<path> when no
	// mapping matches (FR-10.11).
	ServeFiles bool `yaml:"serveFiles"`
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
	// The limits below are off at 0. They protect a shared server from one team using it all up.

	// MaxSessions caps active shadow mocks, key-based and URL-based together.
	MaxSessions int `yaml:"maxSessions"`
	// MaxMappings caps the global mappings, and MaxMappingsPerSession the mappings of one shadow mock.
	MaxMappings           int `yaml:"maxMappings"`
	MaxMappingsPerSession int `yaml:"maxMappingsPerSession"`
	// MaxDelay caps how long a response may be delayed or dribbled. A longer delay in a mapping is
	// refused when the mapping is created over the API, and shortened to this when it is served.
	MaxDelay Duration `yaml:"maxDelay"`
	// MaxConnections caps open connections on each listener.
	MaxConnections int `yaml:"maxConnections"`
	// RequestsPerSecond limits mock traffic from each client address, with Burst requests allowed
	// together (the rate rounded up when Burst is 0).
	RequestsPerSecond float64 `yaml:"requestsPerSecond"`
	Burst             int     `yaml:"burst"`
}

// Mappings configures how mappings sent to the admin API are read (FR-10.9).
type Mappings struct {
	// UnknownFields is reject (a field Shadow Mock does not know is a 422, as in WireMock) or keep
	// (it is stored and returned unchanged, FR-1.4).
	UnknownFields string `yaml:"unknownFields"`
}

// Proxy configures the proxying WireMock's command line sets up (FR-10.13).
type Proxy struct {
	// All sends every request no mapping matches to this URL (--proxy-all).
	All string `yaml:"all"`
	// Record records what is proxied as mappings, written when the server stops (--record-mappings).
	Record bool `yaml:"record"`
	// MatchHeaders are request headers a recording matches on (--match-headers).
	MatchHeaders []string `yaml:"matchHeaders"`
	// PreserveHostHeader sends the client's Host header upstream instead of the target's.
	PreserveHostHeader bool `yaml:"preserveHostHeader"`
	// Via is a proxy server to send proxied requests through, host:port or a URL (--proxy-via).
	Via string `yaml:"via"`
	// Timeout caps one proxied call; 0 means 30 seconds (--proxy-timeout).
	Timeout Duration `yaml:"timeout"`
	// AllowTargets and DenyTargets list the addresses (a, a-b ranges and *.domain names) that may
	// and may not be proxied to or recorded from. The allowed list is looked at first.
	AllowTargets []string `yaml:"allowTargets"`
	DenyTargets  []string `yaml:"denyTargets"`
	// PassThrough is the starting value of the proxyPassThrough setting; true when not set.
	PassThrough *bool `yaml:"passThrough"`
	// Browser lets clients use Shadow Mock as a proxy server (FR-10.15).
	Browser BrowserProxy `yaml:"browser"`
}

// Templating configures response templating (P3).
type Templating struct {
	Global bool `yaml:"global"`
	// Disabled turns templating off altogether, so even a mapping that asks for it is sent as written.
	Disabled bool `yaml:"disabled"`
	// PermittedSystemKeys lists the environment variables the systemValue helper may read.
	PermittedSystemKeys []string `yaml:"permittedSystemKeys"`
	// Extensions lists the template extensions to switch on: jwt (the jwt, claims, claimsObject and jwks
	// helpers) and faker (the random helper).
	Extensions []string `yaml:"extensions"`
	// JWT holds the keys of the jwt helper.
	JWT JWT `yaml:"jwt"`
}

// Egress configures outbound calls made for mappings: webhooks and proxying.
type Egress struct {
	// BlockPrivate refuses private, loopback and link-local destinations (SaaS sets it).
	BlockPrivate bool `yaml:"blockPrivate"`
}

// Log configures logging.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
	// Requests writes one line for each mock request: method, path (never the query), status, layer,
	// detection stage and duration, with the trace id when there is one.
	Requests bool `yaml:"requests"`
}

// Tracing configures OpenTelemetry tracing (NFR-6.3).
type Tracing struct {
	Enabled bool `yaml:"enabled"`
	// Endpoint is the OTLP/HTTP collector, such as http://localhost:4318. Empty uses the standard
	// OTEL_EXPORTER_OTLP_* environment variables.
	Endpoint    string  `yaml:"endpoint"`
	ServiceName string  `yaml:"serviceName"`
	SampleRatio float64 `yaml:"sampleRatio"` // share of new traces kept, 0 to 1
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
	Mappings    Mappings   `yaml:"mappings"`
	Proxy       Proxy      `yaml:"proxy"`
	Templating  Templating `yaml:"templating"`
	Matching    Matching   `yaml:"matching"`
	Egress      Egress     `yaml:"egress"`
	Log         Log        `yaml:"log"`
	Tracing     Tracing    `yaml:"tracing"`
	TLS         TLS        `yaml:"tls"`
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
			Journal:  Journal{Hot: HotJournal{Driver: "memory", MaxEntries: 10000, MaxBytes: 512 << 20}, Warm: Driver{Driver: "none"}},
			Counters: Driver{Driver: "memory"},
			Blobs:    Blobs{Driver: "fs", Dir: "./blobs"},
		},
		Respond:  Respond{Gzip: "auto"},
		Mappings: Mappings{UnknownFields: "reject"},
		Server:   Server{ServeFiles: true},
		Auth:     Auth{Driver: "noop"},
		Tenancy:  Tenancy{Directory: "static"},
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
		Limits:  Limits{MaxBodyBytes: 10 << 20},
		Log:     Log{Level: "info", Format: "text"},
		Tracing: Tracing{ServiceName: "shadowmock", SampleRatio: 1},
	}
}

// Flags are the command-line options that are not configuration.
type Flags struct {
	// Warnings are things to tell the operator at start-up, such as a WireMock flag that is ignored.
	Warnings    []string
	ShowVersion bool
	// HealthCheck asks a running server on this configuration whether it is healthy, and exits.
	HealthCheck bool
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
	applyWireMock := wmFlags(fl)
	args = optionalArgs(args)
	var f Flags
	fl.BoolVar(&f.ShowVersion, "version", false, "print the version and exit")
	fl.BoolVar(&f.HealthCheck, "healthcheck", false, "check that a server on this configuration is healthy, then exit 0 or 1")
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
	warnings, err := applyWireMock(&cfg)
	if err != nil {
		return Config{}, f, err
	}
	f.Warnings = warnings
	cfg.applyBindAddress()
	if err := cfg.Validate(); err != nil {
		return Config{}, f, err
	}
	return cfg, f, nil
}

// applyBindAddress puts listen.bindAddress in front of a listener address that names only a port.
func (c *Config) applyBindAddress() {
	if c.Listen.BindAddress == "" {
		return
	}
	for _, a := range []*string{&c.Listen.Mock, &c.Listen.Admin} {
		if strings.HasPrefix(*a, ":") {
			*a = net.JoinHostPort(c.Listen.BindAddress, strings.TrimPrefix(*a, ":"))
		}
	}
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
		if err := ResolveSecrets(&cfg, lookup); err != nil {
			return Config{}, err
		}
	}
	cfg.applyBindAddress()
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
		"SHADOWMOCK_MODE":             &cfg.Mode,
		"SHADOWMOCK_LISTEN_MOCK":      &cfg.Listen.Mock,
		"SHADOWMOCK_LISTEN_ADMIN":     &cfg.Listen.Admin,
		"SHADOWMOCK_ROOT":             &cfg.Root,
		"SHADOWMOCK_LOG_LEVEL":        &cfg.Log.Level,
		"SHADOWMOCK_LOG_FORMAT":       &cfg.Log.Format,
		"SHADOWMOCK_PUBLIC_BASE_URL":  &cfg.Server.PublicBaseURL,
		"SHADOWMOCK_TRACING_ENDPOINT": &cfg.Tracing.Endpoint,
	}
	for k, dst := range strs {
		if v, ok := lookup(k); ok && v != "" {
			*dst = v
		}
	}
	bools := map[string]*bool{"SHADOWMOCK_TRACING_ENABLED": &cfg.Tracing.Enabled, "SHADOWMOCK_LOG_REQUESTS": &cfg.Log.Requests}
	for k, dst := range bools {
		if v, ok := lookup(k); ok && v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			*dst = b
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
		oneOf("auth.driver", c.Auth.Driver, "noop", "token", "basic"),
		oneOf("tenancy.directory", c.Tenancy.Directory, "static"),
		oneOf("respond.gzip", c.Respond.Gzip, "auto", "off"),
		oneOf("mappings.unknownFields", c.Mappings.UnknownFields, "reject", "keep"),
		oneOf("sessions.detect.ambiguity", c.Sessions.Detect.Ambiguity, "first", "reject", "global"),
		c.validateAuth(),
		c.validateTLS(),
		c.Proxy.Browser.validate(c.Proxy.Via),
		c.validateExtensions(),
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
	case c.Storage.Journal.Hot.MaxBodyBytes < 0 || c.Storage.Journal.Hot.MaxBytes < 0:
		return errors.New("storage.journal.hot.maxBodyBytes and maxBytes must not be negative")
	case c.Limits.negative():
		return errors.New("limits must not be negative; use 0 for no limit")
	case c.Tracing.SampleRatio < 0 || c.Tracing.SampleRatio > 1:
		return errors.New("tracing.sampleRatio must be between 0 and 1")
	case c.Tracing.Endpoint != "" && !strings.HasPrefix(c.Tracing.Endpoint, "http://") && !strings.HasPrefix(c.Tracing.Endpoint, "https://"):
		return errors.New("tracing.endpoint must be an http or https URL, such as http://localhost:4318")
	case c.Tracing.Enabled && c.Tracing.ServiceName == "":
		return errors.New("tracing.serviceName must not be empty when tracing is on")
	case c.Storage.Journal.Hot.MaxEntries < 0:
		return errors.New("storage.journal.hot.maxEntries must not be negative")
	}
	return nil
}

// negative reports whether any limit is below zero.
func (l Limits) negative() bool {
	return l.MaxBodyBytes < 0 || l.MaxSessions < 0 || l.MaxMappings < 0 || l.MaxMappingsPerSession < 0 ||
		time.Duration(l.MaxDelay) < 0 || l.MaxConnections < 0 || l.RequestsPerSecond < 0 || l.Burst < 0
}
