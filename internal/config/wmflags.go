package config

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WireMock's command-line flags (FR-10.13). Every one is accepted, so a command line or a Docker
// command copied from a WireMock setup starts. Each is one of two kinds: implemented, by setting
// the configuration key that does the same; or accepted with a warning, because it tunes the Java
// server and has nothing to tune here.

// wmFlag is one WireMock flag.
type wmFlag struct {
	name string
	// bool means the flag takes no value; optional means it may take one.
	bool, optional bool
	// apply changes the configuration; nil for a flag that is warned about or refused.
	apply func(c *Config, v string) error
	// note, when set, gives a warning about the value the flag was given.
	note func(v string) string
	// why, for a flag with no apply, says why nothing happens: "ignored" flags warn.
	why string
}

const ignored = "ignored"

func setBool(f func(c *Config, on bool)) func(*Config, string) error {
	return func(c *Config, v string) error {
		on := true
		if v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%q is not true or false", v)
			}
			on = b
		}
		f(c, on)
		return nil
	}
}

func setInt(f func(c *Config, n int)) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return fmt.Errorf("%q is not a whole number of 0 or more", v)
		}
		f(c, n)
		return nil
	}
}

func list(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wireMockFlags lists every WireMock flag except --port, --root-dir, --verbose, --version and
// --help, which Resolve defines itself.
func wireMockFlags() []wmFlag {
	return append([]wmFlag{
		// Implemented.
		{name: "global-response-templating", bool: true, apply: func(c *Config, _ string) error { c.Templating.Global = true; return nil }},
		{name: "local-response-templating", bool: true, apply: func(c *Config, _ string) error { c.Templating.Global = false; return nil }},
		{name: "disable-response-templating", bool: true, apply: func(c *Config, _ string) error { c.Templating.Disabled = true; return nil }},
		{name: "enable-stub-cors", bool: true, apply: func(c *Config, _ string) error { c.Server.StubCORS = true; return nil }},
		{name: "disable-gzip", bool: true, apply: func(c *Config, _ string) error { c.Respond.Gzip = "off"; return nil }},
		{name: "no-request-journal", bool: true, apply: func(c *Config, _ string) error { c.Storage.Journal.Hot.MaxEntries = 0; return nil }},
		{name: "max-request-journal-entries", apply: setInt(func(c *Config, n int) { c.Storage.Journal.Hot.MaxEntries = n })},
		{name: "logged-response-body-size-limit", apply: setInt(func(c *Config, n int) { c.Storage.Journal.Hot.MaxBodyBytes = n })},
		{name: "bind-address", apply: func(c *Config, v string) error { c.Listen.BindAddress = v; return nil }},
		{name: "disable-banner", bool: true, apply: func(*Config, string) error { return nil }}, // Shadow Mock prints no banner
		{name: "disable-request-logging", bool: true, apply: func(c *Config, _ string) error { c.Log.Requests = false; return nil }},
		{name: "print-all-network-traffic", bool: true, apply: func(c *Config, _ string) error { c.Log.Requests = true; return nil }},
		{name: "permitted-system-keys", optional: true, apply: func(c *Config, v string) error {
			c.Templating.PermittedSystemKeys = list(v)
			return nil
		}},
		{name: "filename-template", apply: func(c *Config, v string) error { c.Storage.Global.FilenameTemplate = v; return nil }},
		{name: "admin-api-basic-auth", apply: func(c *Config, v string) error {
			user, pass, ok := strings.Cut(v, ":")
			if !ok || user == "" || pass == "" {
				return errors.New("give the credentials as username:password")
			}
			c.Auth.Driver, c.Auth.Basic = "basic", BasicAuth{Username: user, Password: pass}
			return nil
		}},
		{name: "proxy-all", apply: func(c *Config, v string) error { c.Proxy.All = v; return nil }},
		{name: "record-mappings", bool: true, apply: func(c *Config, _ string) error { c.Proxy.Record = true; return nil }},
		{name: "match-headers", apply: func(c *Config, v string) error { c.Proxy.MatchHeaders = list(v); return nil }},
		{name: "preserve-host-header", bool: true, apply: func(c *Config, _ string) error { c.Proxy.PreserveHostHeader = true; return nil }},
		{name: "preserve-user-agent-proxy-header", bool: true, apply: func(*Config, string) error { return nil }}, // the User-Agent is always passed on
		{name: "proxy-via", apply: func(c *Config, v string) error { c.Proxy.Via = v; return nil }},
		{name: "proxy-timeout", apply: setInt(func(c *Config, n int) { c.Proxy.Timeout = Duration(time.Duration(n) * time.Millisecond) })},
		{name: "allow-proxy-targets", apply: func(c *Config, v string) error { c.Proxy.AllowTargets = list(v); return nil }},
		{name: "deny-proxy-targets", apply: func(c *Config, v string) error { c.Proxy.DenyTargets = list(v); return nil }},
		{name: "proxy-pass-through", apply: setBool(func(c *Config, on bool) { c.Proxy.PassThrough = &on })},

		{name: "https-port", apply: setInt(func(c *Config, n int) { c.TLS.Port = n })},
		{name: "disable-http", bool: true, apply: func(c *Config, _ string) error { c.TLS.DisableHTTP = true; return nil }},
		{name: "https-keystore", apply: func(c *Config, v string) error { c.TLS.Keystore = v; return nil }},
		{name: "keystore-type", apply: func(c *Config, v string) error { c.TLS.KeystoreType = v; return nil }},
		{name: "keystore-password", apply: func(c *Config, v string) error { c.TLS.KeystorePassword = v; return nil }},
		{name: "key-manager-password", apply: func(c *Config, v string) error { c.TLS.KeyManagerPassword = v; return nil }},
		{name: "https-truststore", apply: func(c *Config, v string) error { c.TLS.Truststore = v; return nil }},
		{name: "truststore-type", apply: func(c *Config, v string) error { c.TLS.TruststoreType = v; return nil }},
		{name: "truststore-password", apply: func(c *Config, v string) error { c.TLS.TruststorePassword = v; return nil }},
		{name: "https-require-client-cert", bool: true, apply: func(c *Config, _ string) error { c.TLS.RequireClientCert = true; return nil }},
		{name: "disable-http2-plain", bool: true, apply: func(c *Config, _ string) error { c.TLS.DisableHTTP2Plain = true; return nil }},
		{name: "disable-http2-tls", bool: true, apply: func(c *Config, _ string) error { c.TLS.DisableHTTP2TLS = true; return nil }},
		{name: "admin-api-require-https", bool: true, apply: func(c *Config, _ string) error { c.TLS.AdminRequireHTTPS = true; return nil }},

		// Accepted with a warning: they tune the Java server.
		{name: "container-threads", why: ignored},
		{name: "jetty-accept-queue-size", why: ignored},
		{name: "jetty-acceptor-threads", why: ignored},
		{name: "jetty-header-buffer-size", why: ignored},
		{name: "jetty-header-request-size", why: ignored},
		{name: "jetty-header-response-size", why: ignored},
		{name: "jetty-idle-timeout", why: ignored},
		{name: "jetty-stop-timeout", why: ignored},
		{name: "async-response-enabled", why: ignored},
		{name: "async-response-threads", why: ignored},
		{name: "max-http-client-connections", why: ignored},
		{name: "disable-connection-reuse", why: ignored},
		{name: "disable-extensions-scanning", bool: true, why: ignored},
		{name: "disable-optimize-xml-factories-loading", bool: true, why: ignored},
		{name: "disable-strict-http-headers", why: ignored},
		{name: "extensions", apply: func(c *Config, v string) error {
			known := javaExtensions()
			for _, class := range list(v) {
				if ext, ok := known[class]; ok {
					c.enable(ext)
				}
			}
			return nil
		}, note: unknownExtensions},
		{name: "supported-proxy-encodings", why: ignored},
		{name: "use-chunked-encoding", why: ignored},
		{name: "timeout", optional: true, why: ignored},
		{name: "webhook-threadpool-size", why: ignored},
		{name: "max-template-cache-entries", optional: true, why: ignored},
		{name: "load-resources-from-classpath", why: ignored},
	}, browserProxyFlags()...)
}

// browserProxyFlags are the flags that turn on browser proxying and say whom it trusts (FR-10.15).
func browserProxyFlags() []wmFlag {
	return []wmFlag{
		{name: "enable-browser-proxying", bool: true, apply: func(c *Config, _ string) error { c.Proxy.Browser.Enabled = true; return nil }},
		{name: "ca-keystore", apply: func(c *Config, v string) error { c.Proxy.Browser.CAKeystore = v; return nil }},
		{name: "ca-keystore-password", apply: func(c *Config, v string) error { c.Proxy.Browser.CAKeystorePassword = v; return nil }},
		{name: "ca-keystore-type", apply: func(c *Config, v string) error { c.Proxy.Browser.CAKeystoreType = v; return nil }},
		{name: "trust-all-proxy-targets", bool: true, apply: func(c *Config, _ string) error { c.Proxy.Browser.TrustAll = true; return nil }},
		{name: "trust-proxy-target", apply: func(c *Config, v string) error {
			c.Proxy.Browser.TrustTargets = append(c.Proxy.Browser.TrustTargets, list(v)...)
			return nil
		}},
	}
}

// wmFlags registers WireMock's flags on fl. After parsing, the returned function applies the ones
// that were given to a configuration, in the order they were given, and returns the warnings.
func wmFlags(fl *flag.FlagSet) func(c *Config) ([]string, error) {
	type given struct {
		f wmFlag
		v string
	}
	var seen []given
	known := map[string]wmFlag{}
	for _, f := range wireMockFlags() {
		known[f.name] = f
		record := func(v string) { seen = append(seen, given{f, v}) }
		if f.bool {
			fl.BoolFunc(f.name, "WireMock flag; see docs/guide/11-configuration.md", func(string) error { record(""); return nil })
			continue
		}
		fl.Func(f.name, "WireMock flag; see docs/guide/11-configuration.md", func(v string) error { record(v); return nil })
	}
	return func(c *Config) ([]string, error) {
		var warnings []string
		for _, g := range seen {
			if g.f.apply == nil {
				warnings = append(warnings, fmt.Sprintf("--%s is accepted and ignored: it tunes WireMock's Java server, and Shadow Mock has nothing to tune", g.f.name))
				continue
			}
			if err := g.f.apply(c, g.v); err != nil {
				return nil, fmt.Errorf("--%s: %w", g.f.name, err)
			}
			if g.f.note != nil {
				if w := g.f.note(g.v); w != "" {
					warnings = append(warnings, w)
				}
			}
		}
		return warnings, nil
	}
}

// optionalArgs rewrites "--flag value" to "--flag=value" for the flags whose value is optional, so
// "--timeout 5000" and a bare "--timeout" both read as WireMock reads them. A value is an argument
// that does not start with a dash.
func optionalArgs(args []string) []string {
	optional := map[string]bool{}
	for _, f := range wireMockFlags() {
		if f.optional {
			optional["--"+f.name] = true
			optional["-"+f.name] = true
		}
	}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if optional[a] {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				a += "=" + args[i+1]
				i++
			} else {
				a += "="
			}
		}
		out = append(out, a)
	}
	return out
}

// unknownExtensions warns about the classes given to --extensions that no extension of Shadow Mock stands in for.
func unknownExtensions(v string) string {
	known := javaExtensions()
	var unknown []string
	for _, class := range list(v) {
		if _, ok := known[class]; !ok {
			unknown = append(unknown, class)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	return fmt.Sprintf("--extensions: %s is not an extension Shadow Mock has and is ignored; it has jwt, faker and graphql (templating.extensions and matching.extensions), and Go matchers through pkg/extension", strings.Join(unknown, ", "))
}
