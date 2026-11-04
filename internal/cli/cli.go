// Package cli is the command line over the admin API (FR-11.17): `shadowmock session create`, `journal tail`,
// `recording start` and the like. Each sub-command makes exactly one admin call and prints the answer, so it
// holds no behaviour the API lacks. The calls are listed in Commands, which the tests check against the API's
// description.
//
// A key or token is read from the environment, never from a flag, so that it does not show in a process list.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environment variables the command line reads.
const (
	// EnvURL is the base URL of the server, such as "https://mocks.example.com/acme".
	EnvURL = "SHADOWMOCK_URL"
	// EnvKey is a Tool app key (smt_…), sent as a bearer token.
	EnvKey = "SHADOWMOCK_KEY"
	// EnvToken is an admin token or a token from a trusted issuer, sent as a bearer token.
	EnvToken = "SHADOWMOCK_TOKEN"
)

const (
	defaultURL         = "http://localhost:8080"
	defaultAdminPrefix = "/__admin"
	// maxAnswer bounds how much of an answer is copied out.
	maxAnswer = 64 << 20
)

// Command is one sub-command and the one admin call it makes. Route is relative to the admin prefix; {id} and
// {kind} stand for the argument and the --kind flag.
type Command struct {
	Name   string
	Method string
	Route  string
}

// Commands lists every sub-command and the admin call behind it.
//
//nolint:gochecknoglobals // a read-only table that the console and the acceptance steps check against the admin API
var Commands = []Command{
	{"session create", http.MethodPost, "/sessions/{kind}"},
	{"session list", http.MethodGet, "/sessions"},
	{"session delete", http.MethodDelete, "/sessions/{id}"},
	{"journal tail", http.MethodGet, "/requests"},
	{"recording start", http.MethodPost, "/recordings/start"},
	{"recording stop", http.MethodPost, "/recordings/stop"},
	{"recording status", http.MethodGet, "/recordings/status"},
}

// Env is what a run needs from outside.
type Env struct {
	// Lookup reads an environment variable.
	Lookup func(string) (string, bool)
	// Stdout receives the answer of the admin API.
	Stdout io.Writer
	// Client sends the call. Nil means a client with a 30 second timeout.
	Client *http.Client
}

// IsCommand reports whether args begin with a sub-command rather than a server flag.
func IsCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, c := range Commands {
		if strings.HasPrefix(c.Name, args[0]+" ") {
			return true
		}
	}
	return false
}

// call is one admin call about to be made.
type call struct {
	method string
	route  string
	query  url.Values
	body   string
}

// Run runs the sub-command named by args. The answer of the admin API is written to Stdout whatever its status, and an
// answer of 400 or above is also returned as an error, so that the exit status shows the failure.
func Run(ctx context.Context, args []string, env Env) error {
	if len(args) < 2 {
		return usage("a sub-command is needed")
	}
	name := args[0] + " " + args[1]
	fs := flag.NewFlagSet("shadowmock "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	baseURL := fs.String("url", "", "base URL of the server (or "+EnvURL+")")
	prefix := fs.String("admin-prefix", defaultAdminPrefix, "path the admin API is served under")

	build, err := definition(name, fs)
	if err != nil {
		return err
	}
	positional, err := parse(fs, args[2:])
	if err != nil {
		return usage("%s: %v", name, err)
	}
	c, err := build(positional)
	if err != nil {
		return err
	}
	base, err := resolveBase(*baseURL, env.Lookup)
	if err != nil {
		return err
	}
	return send(ctx, env, base+strings.TrimRight(*prefix, "/")+c.route, c)
}

// definition declares the flags of a sub-command and returns what turns its arguments into a call.
func definition(name string, fs *flag.FlagSet) (func([]string) (call, error), error) {
	switch name {
	case "session create":
		return sessionCreate(fs), nil
	case "session list":
		return sessionList(fs), nil
	case "session delete":
		return func(pos []string) (call, error) {
			if len(pos) != 1 {
				return call{}, usage("session delete needs the id of one session")
			}
			return call{method: http.MethodDelete, route: "/sessions/" + url.PathEscape(pos[0])}, nil
		}, nil
	case "journal tail":
		return journalTail(fs), nil
	case "recording start":
		return recordingStart(fs), nil
	case "recording stop":
		return noArguments(name, http.MethodPost, "/recordings/stop"), nil
	case "recording status":
		return noArguments(name, http.MethodGet, "/recordings/status"), nil
	}
	return nil, usage("%q is not a sub-command", name)
}

func sessionCreate(fs *flag.FlagSet) func([]string) (call, error) {
	kind := fs.String("kind", "key-based", "key-based or url-based")
	sname := fs.String("name", "", "a name for the shadow mock")
	ttl := fs.Duration("ttl", 0, "how long it lasts, such as 10m")
	noGlobal := fs.Bool("no-global", false, "answer only from the shadow mock's own mappings, never from the global session")
	var tokens stringList
	fs.Var(&tokens, "token", "a match token (key-based only; repeat for more)")
	return func(pos []string) (call, error) {
		if len(pos) > 0 {
			return call{}, usage("session create takes no argument, got %q", pos[0])
		}
		if *kind != "key-based" && *kind != "url-based" {
			return call{}, usage("--kind must be key-based or url-based")
		}
		if len(tokens) > 0 && *kind != "key-based" {
			return call{}, usage("--token is for key-based shadow mocks")
		}
		if *ttl < 0 {
			return call{}, usage("--ttl cannot be negative")
		}
		body := map[string]any{}
		if *sname != "" {
			body["name"] = *sname
		}
		if *ttl > 0 {
			body["ttlSeconds"] = int64(math.Ceil(ttl.Seconds()))
		}
		if len(tokens) > 0 {
			body["tokens"] = []string(tokens)
		}
		if *noGlobal {
			body["inheritGlobal"] = false
		}
		return jsonCall(http.MethodPost, "/sessions/"+*kind, nil, body)
	}
}

func sessionList(fs *flag.FlagSet) func([]string) (call, error) {
	kind := fs.String("kind", "", "only key-based or url-based")
	state := fs.String("state", "", "only active or expired")
	sname := fs.String("name", "", "only names containing this")
	createdBy := fs.String("created-by", "", "only those made by this person or key")
	return func(pos []string) (call, error) {
		if len(pos) > 0 {
			return call{}, usage("session list takes no argument, got %q", pos[0])
		}
		q := url.Values{}
		set(q, "kind", *kind)
		set(q, "state", *state)
		set(q, "name", *sname)
		set(q, "createdBy", *createdBy)
		return call{method: http.MethodGet, route: "/sessions", query: q}, nil
	}
}

func journalTail(fs *flag.FlagSet) func([]string) (call, error) {
	unmatched := fs.Bool("unmatched", false, "only requests that matched no mapping")
	limit := fs.Int("limit", 20, "how many entries, newest first")
	return func(pos []string) (call, error) {
		if len(pos) > 0 {
			return call{}, usage("journal tail takes no argument, got %q", pos[0])
		}
		if *limit < 1 {
			return call{}, usage("--limit must be at least 1")
		}
		q := url.Values{"limit": {strconv.Itoa(*limit)}}
		if *unmatched {
			q.Set("unmatched", "true")
		}
		return call{method: http.MethodGet, route: "/requests", query: q}, nil
	}
}

func recordingStart(fs *flag.FlagSet) func([]string) (call, error) {
	target := fs.String("target", "", "base URL of the service to record")
	return func(pos []string) (call, error) {
		if len(pos) > 0 {
			return call{}, usage("recording start takes no argument, got %q", pos[0])
		}
		if *target == "" {
			return call{}, usage("recording start needs --target")
		}
		return jsonCall(http.MethodPost, "/recordings/start", nil, map[string]any{"targetBaseUrl": *target})
	}
}

func jsonCall(method, route string, q url.Values, body any) (call, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return call{}, fmt.Errorf("encode the request body: %w", err)
	}
	return call{method: method, route: route, query: q, body: string(b)}, nil
}

func noArguments(name, method, route string) func([]string) (call, error) {
	return func(pos []string) (call, error) {
		if len(pos) > 0 {
			return call{}, usage("%s takes no argument, got %q", name, pos[0])
		}
		return call{method: method, route: route}, nil
	}
}

func set(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}

// parse reads flags that come before or after the positional arguments, which the flag package alone does not.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// resolveBase is the server's base URL without a trailing slash: the flag, else SHADOWMOCK_URL, else the default.
func resolveBase(flagValue string, lookup func(string) (string, bool)) (string, error) {
	raw := flagValue
	if raw == "" {
		raw, _ = lookup(EnvURL)
	}
	if raw == "" {
		raw = defaultURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", usage("the server URL %q must be an http or https URL", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", usage("the server URL %q must have no credentials, query or fragment", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// credential is the key or token to send, if any. A key wins over a token.
func credential(lookup func(string) (string, bool)) string {
	for _, name := range []string{EnvKey, EnvToken} {
		if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func send(ctx context.Context, env Env, target string, c call) error {
	if len(c.query) > 0 {
		target += "?" + c.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, c.method, target, strings.NewReader(c.body))
	if err != nil {
		return fmt.Errorf("build the admin call: %w", err)
	}
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cred := credential(env.Lookup); cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	client := env.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", c.method, req.URL.Redacted(), err)
	}
	defer resp.Body.Close()
	n, err := io.Copy(env.Stdout, io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("read the answer: %w", err)
	}
	if n > 0 {
		if _, err = io.WriteString(env.Stdout, "\n"); err != nil {
			return fmt.Errorf("write the answer: %w", err)
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%s %s: the admin API answered %s", c.method, req.URL.Redacted(), resp.Status)
	}
	return nil
}

// stringList is a flag that can be given more than once.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

// Set adds one value, so that the flag can be given more than once.
func (s *stringList) Set(v string) error {
	if v == "" {
		return errors.New("a value is needed")
	}
	*s = append(*s, v)
	return nil
}

func usage(format string, a ...any) error {
	return fmt.Errorf("usage: shadowmock <session|journal|recording> <command> [flags]: "+format, a...)
}
