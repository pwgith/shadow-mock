package proxy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/egress"
	"github.com/pwgith/shadow-mock/internal/match"
	"github.com/pwgith/shadow-mock/internal/proxy"
)

type upstreamCall struct {
	method, target, body string
	header               http.Header
}

func upstream(t *testing.T, h func(w http.ResponseWriter)) (*httptest.Server, chan upstreamCall) {
	t.Helper()
	calls := make(chan upstreamCall, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls <- upstreamCall{r.Method, r.URL.RequestURI(), string(b), r.Header.Clone()}
		h(w)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func request(t *testing.T, method, target, body string, headers ...string) *match.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), method, "http://mock.example"+target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	return match.NewRequest(r, []byte(body), r.URL.EscapedPath())
}

func TestForwardRelaysRequestAndResponse(t *testing.T) {
	t.Parallel()
	srv, calls := upstream(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Up", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	f := proxy.New(egress.New(egress.Options{}))
	res, err := f.Forward(t.Context(), request(t, http.MethodPost, "/a/b?x=1&y=2", "payload", "X-Keep", "k", "X-Secret", "s", "Accept-Encoding", "gzip", "Connection", "close"), srv.URL+"/", proxy.Options{
		AdditionalHeaders: map[string]string{"X-Added": "yes"},
		RemoveHeaders:     []string{"x-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated || string(res.Body) != `{"ok":true}` || res.Header.Get("X-Up") != "1" {
		t.Errorf("result = %d %q %v", res.Status, res.Body, res.Header)
	}
	if res.Header.Get("Content-Length") != "" || res.Header.Get("Transfer-Encoding") != "" {
		t.Errorf("the transfer headers were relayed: %v", res.Header)
	}
	c := <-calls
	if c.method != http.MethodPost || c.target != "/a/b?x=1&y=2" || c.body != "payload" {
		t.Errorf("upstream got %s %s %q", c.method, c.target, c.body)
	}
	if c.header.Get("X-Keep") != "k" || c.header.Get("X-Added") != "yes" || c.header.Get("X-Secret") != "" {
		t.Errorf("upstream headers = %v", c.header)
	}
	if c.header.Get("Connection") == "close" {
		t.Error("a hop-by-hop header was forwarded")
	}
}

func TestForwardRemovesAPathPrefix(t *testing.T) {
	t.Parallel()
	srv, calls := upstream(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) })
	f := proxy.New(egress.New(egress.Options{}))
	for path, want := range map[string]string{"/api/payments/1": "/payments/1", "/api": "/", "/other": "/other"} {
		if _, err := f.Forward(t.Context(), request(t, http.MethodGet, path, ""), srv.URL, proxy.Options{PrefixToRemove: "/api"}); err != nil {
			t.Fatal(err)
		}
		if c := <-calls; c.target != want {
			t.Errorf("%s forwarded as %s, want %s", path, c.target, want)
		}
	}
}

func TestForwardErrors(t *testing.T) {
	t.Parallel()
	srv, _ := upstream(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) })
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	tests := []struct {
		name   string
		client *http.Client
		base   string
		code   string
	}{
		{"not a URL", egress.New(egress.Options{}), "payments", proxy.CodeBadGateway},
		{"no such server", egress.New(egress.Options{}), deadURL, proxy.CodeBadGateway},
		{"private address in SaaS", egress.New(egress.Options{BlockPrivate: true}), srv.URL, proxy.CodeBlocked},
	}
	for _, tt := range tests {
		_, err := proxy.New(tt.client).Forward(context.Background(), request(t, http.MethodGet, "/x", ""), tt.base, proxy.Options{})
		var pe *proxy.Error
		if !errors.As(err, &pe) || pe.Code != tt.code {
			t.Errorf("%s: error = %v, want code %s", tt.name, err, tt.code)
		}
	}
}

func TestForwardDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	srv, _ := upstream(t, func(w http.ResponseWriter) {
		w.Header().Set("Location", "http://169.254.169.254/")
		w.WriteHeader(http.StatusFound)
	})
	res, err := proxy.New(egress.New(egress.Options{})).Forward(t.Context(), request(t, http.MethodGet, "/x", ""), srv.URL, proxy.Options{})
	if err != nil || res.Status != http.StatusFound || res.Header.Get("Location") == "" {
		t.Errorf("Forward = %+v, %v; want the redirect passed on", res, err)
	}
}
