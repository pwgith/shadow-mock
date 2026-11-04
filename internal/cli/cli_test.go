package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/cli"
	"github.com/pwgith/shadow-mock/test/contract"
)

// seen is a call the test server received.
type seen struct {
	method, path, query, auth, body string
}

// server answers every call with status and body, and records what it got.
func server(t *testing.T, status int, body string) (*httptest.Server, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, seen{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), string(b)})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func env(vars map[string]string, out io.Writer) cli.Env {
	return cli.Env{Lookup: func(k string) (string, bool) { v, ok := vars[k]; return v, ok }, Stdout: out}
}

func TestEachCommandMakesOneCallThatTheAPIDescribes(t *testing.T) {
	spec, err := contract.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cli.Commands {
		routes := []string{c.Route}
		if strings.Contains(c.Route, "{kind}") {
			routes = []string{strings.Replace(c.Route, "{kind}", "key-based", 1), strings.Replace(c.Route, "{kind}", "url-based", 1)}
		}
		for _, r := range routes {
			path := "/__admin" + strings.ReplaceAll(r, "{id}", "S1")
			if spec.FindOperation(c.Method, path) == nil {
				t.Errorf("%q calls %s %s, which the API does not describe", c.Name, c.Method, path)
			}
		}
	}
}

func TestIsCommand(t *testing.T) {
	for args, want := range map[string]bool{
		"session list": true, "journal tail": true, "recording stop": true,
		"-version": false, "--config x.yaml": false, "": false, "sessions": false,
	} {
		if got := cli.IsCommand(strings.Fields(args)); got != want {
			t.Errorf("IsCommand(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestSubCommandsMakeTheDocumentedCall(t *testing.T) {
	tests := []struct {
		args   string
		method string
		path   string
		query  string
		body   string
	}{
		{"session create", "POST", "/__admin/sessions/key-based", "", `{}`},
		{"session create --kind key-based --ttl 10m", "POST", "/__admin/sessions/key-based", "", `{"ttlSeconds":600}`},
		{"session create --kind url-based --name demo", "POST", "/__admin/sessions/url-based", "", `{"name":"demo"}`},
		{"session create --kind url-based --no-global", "POST", "/__admin/sessions/url-based", "", `{"inheritGlobal":false}`},
		{"session create --kind key-based --token ABC-123 --token DEF-456", "POST", "/__admin/sessions/key-based", "", `{"tokens":["ABC-123","DEF-456"]}`},
		{"session list --state active", "GET", "/__admin/sessions", "state=active", ""},
		{"session list --kind key-based --name x --created-by me", "GET", "/__admin/sessions", "createdBy=me&kind=key-based&name=x", ""},
		{"session delete S1", "DELETE", "/__admin/sessions/S1", "", ""},
		{"session delete --url URL S1", "DELETE", "/__admin/sessions/S1", "", ""},
		{"session delete a/b", "DELETE", "/__admin/sessions/a%2Fb", "", ""},
		{"journal tail --unmatched", "GET", "/__admin/requests", "limit=20&unmatched=true", ""},
		{"journal tail --limit 5", "GET", "/__admin/requests", "limit=5", ""},
		{"recording start --target http://localhost:9000", "POST", "/__admin/recordings/start", "", `{"targetBaseUrl":"http://localhost:9000"}`},
		{"recording stop", "POST", "/__admin/recordings/stop", "", ""},
		{"recording status", "GET", "/__admin/recordings/status", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.args, func(t *testing.T) {
			srv, calls := server(t, 200, `{"answer":1}`)
			var out bytes.Buffer
			args := strings.Fields(strings.ReplaceAll(tc.args, "URL", srv.URL))
			if !strings.Contains(tc.args, "--url") {
				args = append(args, "--url", srv.URL)
			}
			if err := cli.Run(context.Background(), args, env(nil, &out)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(*calls) != 1 {
				t.Fatalf("made %d calls, want 1", len(*calls))
			}
			got := (*calls)[0]
			if got.method != tc.method || got.path != tc.path || got.query != tc.query || got.body != tc.body {
				t.Errorf("call = %+v, want %s %s ?%s body %q", got, tc.method, tc.path, tc.query, tc.body)
			}
			if out.String() != "{\"answer\":1}\n" {
				t.Errorf("output = %q, want the API's answer", out.String())
			}
		})
	}
}

func TestTheURLComesFromTheEnvironmentAndKeepsItsPath(t *testing.T) {
	srv, calls := server(t, 200, "{}")
	var out bytes.Buffer
	vars := map[string]string{cli.EnvURL: srv.URL + "/acme/"}
	if err := cli.Run(context.Background(), []string{"session", "list", "--admin-prefix", "/api"}, env(vars, &out)); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].path; got != "/acme/api/sessions" {
		t.Errorf("path = %q, want /acme/api/sessions", got)
	}
}

func TestTheKeyOrTokenIsSentAsABearerToken(t *testing.T) {
	for name, tc := range map[string]struct {
		vars map[string]string
		want string
	}{
		"none":               {nil, ""},
		"key":                {map[string]string{cli.EnvKey: "smt_KEY"}, "Bearer smt_KEY"},
		"token":              {map[string]string{cli.EnvToken: "tok"}, "Bearer tok"},
		"the key wins":       {map[string]string{cli.EnvKey: "smt_KEY", cli.EnvToken: "tok"}, "Bearer smt_KEY"},
		"blank is not a key": {map[string]string{cli.EnvKey: "  ", cli.EnvToken: "tok"}, "Bearer tok"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := server(t, 200, "{}")
			vars := map[string]string{cli.EnvURL: srv.URL}
			for k, v := range tc.vars {
				vars[k] = v
			}
			if err := cli.Run(context.Background(), []string{"session", "list"}, env(vars, io.Discard)); err != nil {
				t.Fatal(err)
			}
			if got := (*calls)[0].auth; got != tc.want {
				t.Errorf("Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnErrorAnswerIsPrintedAndFailsTheCommand(t *testing.T) {
	srv, _ := server(t, 404, `{"error":"not_found"}`)
	var out bytes.Buffer
	err := cli.Run(context.Background(), []string{"session", "delete", "S1", "--url", srv.URL}, env(nil, &out))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want one that names 404", err)
	}
	if out.String() != "{\"error\":\"not_found\"}\n" {
		t.Errorf("output = %q, want the API's answer", out.String())
	}
}

func TestACallThatCannotBeMadeFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if err := cli.Run(context.Background(), []string{"session", "list", "--url", srv.URL}, env(nil, io.Discard)); err == nil {
		t.Error("a refused connection did not fail the command")
	}
}

func TestBadArgumentsAreRefusedBeforeAnyCall(t *testing.T) {
	srv, calls := server(t, 200, "{}")
	for _, args := range []string{
		"session",
		"session create --kind other",
		"session create --kind url-based --token ABCDEF",
		"session create --kind key-based --ttl -1m",
		"session create --kind key-based extra",
		"session delete",
		"session delete a b",
		"session list extra",
		"session list --state",
		"session list --nope",
		"journal tail --limit 0",
		"journal tail extra",
		"recording start",
		"recording stop extra",
		"recording rewind",
	} {
		err := cli.Run(context.Background(), append(strings.Fields(args), "--url", srv.URL), env(nil, io.Discard))
		if err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("%d calls were made for bad arguments", len(*calls))
	}
}

func TestTheServerURLMustBeAPlainHTTPURL(t *testing.T) {
	for _, raw := range []string{"ftp://x", "file:///etc/passwd", "localhost:8080", "http://", "http://user:pw@x", "http://x?a=b", "http://x#f", "://"} {
		err := cli.Run(context.Background(), []string{"session", "list", "--url", raw}, env(nil, io.Discard))
		if err == nil {
			t.Errorf("--url %q was accepted", raw)
		}
		err = cli.Run(context.Background(), []string{"session", "list"}, env(map[string]string{cli.EnvURL: raw}, io.Discard))
		if err == nil {
			t.Errorf("%s=%q was accepted", cli.EnvURL, raw)
		}
	}
}
