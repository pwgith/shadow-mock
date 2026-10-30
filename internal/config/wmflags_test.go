package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/config"
)

// wireMockFlags is every flag in the output of WireMock 3.13.2's --help, other than --port,
// --root-dir, --verbose, --version and --help. A flag WireMock gains is added here, and the test
// then says what Shadow Mock does with it.
var wireMockFlags = map[string]string{ // name -> a value, or "" for a flag without one
	"admin-api-basic-auth": "u:p", "admin-api-require-https": "", "allow-proxy-targets": "10.0.0.1,*.example.com",
	"async-response-enabled": "true", "async-response-threads": "10", "bind-address": "127.0.0.1",
	"ca-keystore": "k", "ca-keystore-password": "p", "ca-keystore-type": "JKS", "container-threads": "10",
	"deny-proxy-targets": "10.0.0.1-10.0.0.9", "disable-banner": "", "disable-connection-reuse": "true",
	"disable-extensions-scanning": "", "disable-gzip": "", "disable-http": "", "disable-http2-plain": "",
	"disable-http2-tls": "", "disable-optimize-xml-factories-loading": "", "disable-request-logging": "",
	"disable-response-templating": "", "disable-strict-http-headers": "true", "enable-browser-proxying": "",
	"enable-stub-cors": "", "extensions": "a.B", "filename-template": "{{{id}}}.json", "global-response-templating": "",
	"https-keystore": "k", "https-port": "8443", "https-require-client-cert": "", "https-truststore": "t",
	"jetty-accept-queue-size": "1", "jetty-acceptor-threads": "1", "jetty-header-buffer-size": "1",
	"jetty-header-request-size": "1", "jetty-header-response-size": "1", "jetty-idle-timeout": "1",
	"jetty-stop-timeout": "1", "key-manager-password": "p", "keystore-password": "p", "keystore-type": "JKS",
	"load-resources-from-classpath": "x", "local-response-templating": "", "logged-response-body-size-limit": "100",
	"match-headers": "A,B", "max-http-client-connections": "5", "max-request-journal-entries": "50",
	"max-template-cache-entries": "5", "no-request-journal": "", "permitted-system-keys": "HOME",
	"preserve-host-header": "", "preserve-user-agent-proxy-header": "", "print-all-network-traffic": "",
	"proxy-all": "http://localhost:9", "proxy-pass-through": "false", "proxy-timeout": "5000", "proxy-via": "proxy:3128",
	"record-mappings": "", "supported-proxy-encodings": "gzip", "timeout": "5000", "trust-all-proxy-targets": "",
	"trust-proxy-target": "x", "truststore-password": "p", "truststore-type": "JKS", "use-chunked-encoding": "never",
	"webhook-threadpool-size": "5",
}

// needsBrowserProxying are the flags that mean something only with browser proxying on.
var needsBrowserProxying = map[string]bool{
	"ca-keystore": true, "ca-keystore-password": true, "ca-keystore-type": true,
	"trust-all-proxy-targets": true, "trust-proxy-target": true,
}

// needsHTTPSPort are the flags that mean something only with an HTTPS listener.
var needsHTTPSPort = map[string]bool{
	"disable-http": true, "https-keystore": true, "keystore-type": true, "keystore-password": true,
	"key-manager-password": true, "https-truststore": true, "truststore-type": true, "truststore-password": true,
	"admin-api-require-https": true, "https-require-client-cert": true,
}

func resolve(t *testing.T, args ...string) (config.Config, config.Flags, error) {
	t.Helper()
	return config.Resolve(args, env())
}

func TestEveryWireMockFlagIsAccepted(t *testing.T) {
	t.Parallel()
	for name, value := range wireMockFlags {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := []string{"--" + name}
			if needsHTTPSPort[name] {
				args = append([]string{"--https-port", "8443"}, args...)
			}
			if needsBrowserProxying[name] {
				args = append([]string{"--enable-browser-proxying"}, args...)
			}
			if name == "https-require-client-cert" {
				args = append(args, "--https-truststore", "t")
			}
			if value != "" {
				args = append(args, value)
			}
			_, _, err := resolve(t, args...)
			if err != nil {
				t.Errorf("--%s was refused: %v", name, err)
			}
		})
	}
}

func TestAFlagThatWireMockLacksIsStillRefused(t *testing.T) {
	t.Parallel()
	if _, _, err := resolve(t, "--no-such-flag"); err == nil {
		t.Error("an unknown flag must stop startup")
	}
}

func TestWireMockFlagsSetTheConfiguration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		check func(c config.Config) bool
	}{
		{"global templating", []string{"--global-response-templating"}, func(c config.Config) bool { return c.Templating.Global }},
		{"templating off", []string{"--disable-response-templating"}, func(c config.Config) bool { return c.Templating.Disabled }},
		{"stub cors", []string{"--enable-stub-cors"}, func(c config.Config) bool { return c.Server.StubCORS }},
		{"gzip off", []string{"--disable-gzip"}, func(c config.Config) bool { return c.Respond.Gzip == "off" }},
		{"no journal", []string{"--no-request-journal"}, func(c config.Config) bool { return c.Storage.Journal.Hot.MaxEntries == 0 }},
		{"journal size", []string{"--max-request-journal-entries", "7"}, func(c config.Config) bool { return c.Storage.Journal.Hot.MaxEntries == 7 }},
		{"journal body size", []string{"--logged-response-body-size-limit", "99"}, func(c config.Config) bool { return c.Storage.Journal.Hot.MaxBodyBytes == 99 }},
		{"bind address", []string{"--port", "9000", "--bind-address", "127.0.0.1"}, func(c config.Config) bool { return c.Listen.Mock == "127.0.0.1:9000" }},
		{"the equals form", []string{"--port=9001", "--bind-address=127.0.0.1"}, func(c config.Config) bool { return c.Listen.Mock == "127.0.0.1:9001" }},
		{"request logging off", []string{"--disable-request-logging"}, func(c config.Config) bool { return !c.Log.Requests }},
		{"all traffic", []string{"--print-all-network-traffic"}, func(c config.Config) bool { return c.Log.Requests }},
		{"system keys", []string{"--permitted-system-keys", "HOME,PATH"}, func(c config.Config) bool { return len(c.Templating.PermittedSystemKeys) == 2 }},
		{"file names", []string{"--filename-template", "{{{id}}}.json"}, func(c config.Config) bool { return c.Storage.Global.FilenameTemplate == "{{{id}}}.json" }},
		{"basic auth", []string{"--admin-api-basic-auth", "u:p:q"}, func(c config.Config) bool {
			return c.Auth.Driver == "basic" && c.Auth.Basic.Username == "u" && c.Auth.Basic.Password == "p:q"
		}},
		{"proxy all and record", []string{"--proxy-all", "http://x", "--record-mappings", "--match-headers", "A, B"}, func(c config.Config) bool {
			return c.Proxy.All == "http://x" && c.Proxy.Record && len(c.Proxy.MatchHeaders) == 2 && c.Proxy.MatchHeaders[1] == "B"
		}},
		{"proxy host header", []string{"--preserve-host-header"}, func(c config.Config) bool { return c.Proxy.PreserveHostHeader }},
		{"proxy via and timeout", []string{"--proxy-via", "p:1", "--proxy-timeout", "2500"}, func(c config.Config) bool {
			return c.Proxy.Via == "p:1" && time.Duration(c.Proxy.Timeout) == 2500*time.Millisecond
		}},
		{"proxy targets", []string{"--allow-proxy-targets", "a,b", "--deny-proxy-targets", "c"}, func(c config.Config) bool {
			return len(c.Proxy.AllowTargets) == 2 && len(c.Proxy.DenyTargets) == 1
		}},
		{"pass through", []string{"--proxy-pass-through", "false"}, func(c config.Config) bool { return c.Proxy.PassThrough != nil && !*c.Proxy.PassThrough }},
		{"a flag with an optional value, alone", []string{"--timeout", "--port", "9002"}, func(c config.Config) bool { return c.Listen.Mock == ":9002" }},
		{"a flag with an optional value, given", []string{"--timeout", "5000", "--port", "9003"}, func(c config.Config) bool { return c.Listen.Mock == ":9003" }},
		{"https", []string{"--https-port", "8443", "--https-keystore", "k.p12", "--keystore-password", "pw", "--disable-http"}, func(c config.Config) bool {
			return c.TLS.Port == 8443 && c.TLS.Keystore == "k.p12" && c.TLS.KeystorePassword == "pw" && c.TLS.DisableHTTP
		}},
		{"http2 off", []string{"--https-port", "8443", "--disable-http2-plain", "--disable-http2-tls", "--admin-api-require-https"}, func(c config.Config) bool {
			return c.TLS.DisableHTTP2Plain && c.TLS.DisableHTTP2TLS && c.TLS.AdminRequireHTTPS
		}},
		{"the last flag wins", []string{"--global-response-templating", "--local-response-templating"}, func(c config.Config) bool { return !c.Templating.Global }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _, err := resolve(t, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(c) {
				t.Errorf("--%s did not set the configuration: %+v", strings.Join(tt.args, " "), c)
			}
		})
	}
}

func TestFlagsThatTuneTheJavaServerWarnOnceEach(t *testing.T) {
	t.Parallel()
	_, f, err := resolve(t, "--container-threads", "20", "--jetty-idle-timeout", "5", "--disable-extensions-scanning", "--global-response-templating")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Warnings) != 3 {
		t.Fatalf("warnings = %v, want one for each of the three ignored flags", f.Warnings)
	}
	for _, w := range f.Warnings {
		if !strings.Contains(w, "ignored") {
			t.Errorf("warning %q should say the flag is ignored", w)
		}
	}
}

func TestWireMockFlagMistakesAreRefusedWithAMessage(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, flag, value, want string }{
		{"credentials without a colon", "admin-api-basic-auth", "justuser", "username:password"},
		{"a count that is not a number", "max-request-journal-entries", "many", "whole number"},
		{"a negative count", "proxy-timeout", "-5", "whole number"},
		{"a pass-through that is not a boolean", "proxy-pass-through", "maybe", "true or false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := resolve(t, "--"+tt.flag, tt.value)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), tt.flag) {
				t.Errorf("err = %v, want one naming --%s and containing %q", err, tt.flag, tt.want)
			}
		})
	}
}

func TestBasicAuthNeedsAUserAndAPassword(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Auth.Driver = "basic"
	if err := c.Validate(); err == nil {
		t.Error("auth.driver: basic without credentials must be refused")
	}
	c.Auth.Basic = config.BasicAuth{Username: "u", PasswordEnv: "MY_PASS"}
	if _, err := config.Load(writeFile(t, "auth:\n  driver: basic\n  basic:\n    username: u\n    passwordEnv: MY_PASS\n"), env()); err == nil {
		t.Error("a passwordEnv that is not set must be an error")
	}
	got, err := config.Load(writeFile(t, "auth:\n  driver: basic\n  basic:\n    username: u\n    passwordEnv: MY_PASS\n"), env("MY_PASS", "s3cret"))
	if err != nil || got.Auth.Basic.Password != "s3cret" {
		t.Errorf("password = %q, err = %v; want it read from MY_PASS", got.Auth.Basic.Password, err)
	}
}
