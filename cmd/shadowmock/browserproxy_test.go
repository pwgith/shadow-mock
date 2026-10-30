package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/config"
)

// proxied is a running Shadow Mock with browser proxying on, and an HTTP client that uses it as its
// proxy server and trusts only the certificate authority it generated.
type proxied struct {
	admin  string // the plain base URL, for the admin API
	client *http.Client
	http   *httptest.Server // an origin over HTTP
	https  *httptest.Server // an origin over HTTPS with a certificate nobody trusts
}

// startProxied serves with change applied to the configuration. The authority is made in a home
// directory of its own, so the test leaves nothing behind.
func startProxied(t *testing.T, change func(*config.BrowserProxy)) *proxied {
	t.Helper()
	caDir := t.TempDir()
	p := &proxied{}
	origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "origin:"+r.Host+r.URL.RequestURI())
	})
	p.http, p.https = httptest.NewServer(origin), httptest.NewTLSServer(origin)
	t.Cleanup(p.http.Close)
	t.Cleanup(p.https.Close)
	plain, _ := serveCfg(t, func(c *config.Config) {
		c.Proxy.Browser.Enabled, c.Proxy.Browser.CADir = true, caDir
		change(&c.Proxy.Browser)
	})
	p.admin = plain
	roots := x509.NewCertPool()
	if pemData, err := os.ReadFile(filepath.Join(caDir, "ca.pem")); err == nil {
		roots.AppendCertsFromPEM(pemData)
	} else {
		t.Fatalf("the certificate authority was not stored: %v", err)
	}
	proxyURL, err := url.Parse(plain)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: false,
	}
	t.Cleanup(tr.CloseIdleConnections)
	p.client = &http.Client{Transport: tr, Timeout: 10 * time.Second}
	return p
}

// get sends a GET through the proxy.
func (p *proxied) get(t *testing.T, target string) (int, string) {
	t.Helper()
	return send(t, p.client, http.MethodGet, target, "")
}

func send(t *testing.T, c *http.Client, method, target, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// direct talks to the admin API without the proxy.
func (p *proxied) direct(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	return send(t, &http.Client{Timeout: 10 * time.Second}, method, p.admin+"/__admin"+path, body)
}

func TestBrowserProxyStubsAndPassesThrough(t *testing.T) {
	p := startProxied(t, func(b *config.BrowserProxy) { b.TrustAll = true })
	if code, body := p.direct(t, http.MethodPost, "/mappings", `{"request":{"urlPath":"/stubbed"},"response":{"status":200,"body":"stubbed"}}`); code != http.StatusCreated {
		t.Fatalf("create mapping: %d %s", code, body)
	}
	httpHost := strings.TrimPrefix(p.http.URL, "http://")
	httpsHost := strings.TrimPrefix(p.https.URL, "https://")

	tests := []struct {
		name, target, want string
	}{
		{"a stub answers a request to any host over HTTP", "http://nowhere.invalid/stubbed", "stubbed"},
		{"a stub answers a request to any host over HTTPS", "https://nowhere.invalid/stubbed", "stubbed"},
		{"an unmatched request goes on to its origin over HTTP", p.http.URL + "/up?x=1", "origin:" + httpHost + "/up?x=1"},
		{"an unmatched request goes on to its origin over HTTPS", p.https.URL + "/up", "origin:" + httpsHost + "/up"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := p.get(t, tt.target)
			if code != http.StatusOK || body != tt.want {
				t.Errorf("got %d %q, want 200 %q", code, body, tt.want)
			}
		})
	}

	t.Run("the journal says the requests came through the proxy and counts a pass-through as matched", func(t *testing.T) {
		_, body := p.direct(t, http.MethodGet, "/requests", "")
		var got struct {
			Requests []struct {
				WasMatched bool `json:"wasMatched"`
				Request    struct {
					URL    string `json:"url"`
					Scheme string `json:"scheme"`
					Proxy  bool   `json:"browserProxyRequest"`
				} `json:"request"`
			} `json:"requests"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		var schemes []string
		for _, r := range got.Requests {
			if !strings.HasPrefix(r.Request.URL, "/up") {
				continue
			}
			if !r.WasMatched || !r.Request.Proxy {
				t.Errorf("want matched and browserProxyRequest, got %+v", r)
			}
			schemes = append(schemes, r.Request.Scheme)
		}
		if len(schemes) != 2 || schemes[0] == schemes[1] {
			t.Errorf("want one http and one https request to /up, got %v in %s", schemes, body)
		}
	})
	t.Run("a request sent straight to the server is not a proxy request", func(t *testing.T) {
		p.direct(t, http.MethodDelete, "/requests", "")
		send(t, &http.Client{Timeout: 5 * time.Second}, http.MethodGet, p.admin+"/direct", "")
		_, body := p.direct(t, http.MethodGet, "/requests", "")
		if strings.Contains(body, `"browserProxyRequest" : true`) || strings.Contains(body, `"browserProxyRequest":true`) {
			t.Errorf("a direct request was marked as a proxy request: %s", body)
		}
	})
}

func TestBrowserProxyChecksTheOriginsCertificate(t *testing.T) {
	t.Run("an untrusted origin is an SSL failure", func(t *testing.T) {
		p := startProxied(t, func(*config.BrowserProxy) {})
		code, body := p.get(t, p.https.URL+"/up")
		if code != http.StatusInternalServerError || !strings.HasPrefix(body, "SSL failure trying to make a proxied request") {
			t.Errorf("got %d %q, want 500 and an SSL failure", code, body)
		}
	})
	t.Run("a named origin is trusted without checking", func(t *testing.T) {
		p := startProxied(t, func(b *config.BrowserProxy) { b.TrustTargets = []string{"127.0.0.1"} })
		if code, body := p.get(t, p.https.URL+"/up"); code != http.StatusOK || !strings.HasPrefix(body, "origin:") {
			t.Errorf("got %d %q", code, body)
		}
	})
	t.Run("an origin that cannot be reached is a network failure", func(t *testing.T) {
		p := startProxied(t, func(*config.BrowserProxy) {})
		code, body := p.get(t, "http://nowhere.invalid/x")
		if code != http.StatusInternalServerError || !strings.HasPrefix(body, "Network failure trying to make a proxied request") {
			t.Errorf("got %d %q, want 500 and a network failure", code, body)
		}
	})
}

func TestConnectIsRefusedWhenBrowserProxyingIsOff(t *testing.T) {
	plain, _ := serveWith(t, func(*config.TLS) {})
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(plain, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("CONNECT with browser proxying off: got %d, want 405", resp.StatusCode)
	}
}
