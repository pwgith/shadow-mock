package acceptance

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// initBrowserProxy registers the steps for using the engine as a proxy server (FR-10.15).
func (w *world) initBrowserProxy(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is running as a browser proxy$`, func() error { return w.browserProxy(false) })
	sc.Step(`^Shadow Mock is running as a browser proxy that trusts every origin$`, func() error { return w.browserProxy(true) })
	sc.Step(`^Shadow Mock is running as a browser proxy that trusts the origin "([^"]*)"$`, func(host string) error {
		return w.reconfigure("proxy: { browser: { enabled: true, caDir: '" + filepath.Join(w.root, "ca") + "', trustTargets: [" + host + "] } }")
	})
	sc.Step(`^the certificate authority is stored as "([^"]*)"$`, func(file string) error {
		if _, err := os.Stat(filepath.Join(w.root, "ca", file)); err != nil {
			return fmt.Errorf("the authority was not stored: %w", err)
		}
		return nil
	})
	sc.Step(`^I send a "([A-Z]+)" request to "([^"]*)" through the proxy$`, func(method, target string) error {
		r := newRequest(method, target)
		r.viaProxy = true
		return w.sendOne(r)
	})
}

// browserProxy restarts the engine with browser proxying on. The authority it makes is kept in the
// scenario's own directory.
func (w *world) browserProxy(trustAll bool) error {
	yaml := "proxy: { browser: { enabled: true, caDir: '" + filepath.Join(w.root, "ca") + "'"
	if trustAll {
		yaml += ", trustAll: true"
	}
	return w.reconfigure(yaml + " } }")
}

// proxyClient is a client that uses the engine as its proxy server. The certificates it is shown
// are not checked here: that the authority signs them is tested where the listeners are started
// (cmd/shadowmock/browserproxy_test.go).
func (w *world) proxyClient() (*http.Client, error) {
	u, err := url.Parse(w.srv.URL)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(u),
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // see proxyClient
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// proxyTarget is the URL a request through the proxy is sent to: as the step wrote it.
func (w *world) proxyTarget(target string) string {
	return strings.TrimSpace(w.sub(target))
}
