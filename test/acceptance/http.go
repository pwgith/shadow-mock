package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ohler55/ojg/jp"
)

// reply is a response the scenario received.
type reply struct {
	status     int
	statusText string
	header     http.Header
	body       []byte
	elapsed    time.Duration
	// chunks counts the reads that returned data, which for a dribbled body is the chunks sent.
	chunks int
}

// request describes one request a step sends.
type request struct {
	method string
	target string
	header http.Header
	host   string
	body   []byte
	basic  [2]string
	cookie [][2]string
	// viaProxy sends the request to its own address through the engine, used as a proxy server.
	viaProxy bool
}

func newRequest(method, target string) *request {
	return &request{method: method, target: target, header: http.Header{}}
}

// resolve turns a step's target into a URL on the running engine. Absolute URLs for
// localhost:8080, the standalone default, are redirected to the in-process server.
func (w *world) resolve(target string) (string, error) {
	target = w.sub(target)
	switch {
	case strings.HasPrefix(target, "/"):
		return w.srv.URL + target, nil
	case strings.HasPrefix(target, "https://shadowmock.test"):
		return w.srv.URL + strings.TrimPrefix(target, "https://shadowmock.test"), nil
	case strings.HasPrefix(target, "http://localhost:8080"):
		return w.srv.URL + strings.TrimPrefix(target, "http://localhost:8080"), nil
	case w.tlsSrv != nil && strings.HasPrefix(target, "https://localhost:"+strconv.Itoa(w.tlsPort)):
		return w.tlsSrv.URL + strings.TrimPrefix(target, "https://localhost:"+strconv.Itoa(w.tlsPort)), nil
	case w.tlsSrv != nil && strings.HasPrefix(target, w.tlsSrv.URL):
		return target, nil
	case w.adminSrv != nil && strings.HasPrefix(target, "http://localhost:9090"):
		return w.adminSrv.URL + strings.TrimPrefix(target, "http://localhost:9090"), nil
	case w.adminSrv != nil && strings.HasPrefix(target, w.adminSrv.URL):
		return target, nil
	case strings.HasPrefix(target, w.srv.URL):
		return target, nil
	default:
		return "", fmt.Errorf("cannot send a request to %q: this harness reaches only the engine under test", target)
	}
}

// destination is the URL r goes to and the client that sends it: the engine under test, or any address
// through the engine when the request goes through the proxy.
func (w *world) destination(r *request) (string, *http.Client, error) {
	if r.viaProxy {
		c, err := w.proxyClient()
		return w.proxyTarget(r.target), c, err
	}
	target, err := w.resolve(r.target)
	return target, w.client, err
}

// send sends r to the engine and records the reply as the scenario's last response.
func (w *world) send(r *request) (*reply, error) {
	if err := w.ensure(); err != nil {
		return nil, err
	}
	target, client, err := w.destination(r)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(context.Background(), r.method, target, bytes.NewReader(r.body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, w.sub(v))
		}
	}
	switch {
	case r.host != "":
		req.Host = w.sub(r.host)
	case w.tlsSrv != nil && strings.HasPrefix(w.sub(r.target), "https://localhost:"):
		req.Host = strings.SplitN(strings.TrimPrefix(w.sub(r.target), "https://"), "/", 2)[0] // so the port matches what the step names
	}
	if r.basic != [2]string{} {
		req.SetBasicAuth(r.basic[0], r.basic[1])
	}
	for _, c := range r.cookie {
		req.AddCookie(&http.Cookie{Name: c[0], Value: w.sub(c[1])})
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, &transportError{fmt.Errorf("%s %s: %w", r.method, target, err)}
	}
	defer resp.Body.Close()
	b, chunks, err := readChunks(resp.Body)
	if err != nil {
		return nil, &transportError{fmt.Errorf("read response of %s %s: %w", r.method, target, err)}
	}
	rep := &reply{
		status: resp.StatusCode, statusText: strings.TrimPrefix(resp.Status, fmt.Sprintf("%d ", resp.StatusCode)),
		header: resp.Header, body: b, elapsed: time.Since(start), chunks: chunks,
	}
	w.sendErr = nil
	w.last = rep
	w.history = append(w.history, rep)
	return rep, nil
}

// adminCall sends an admin request (relative to the configured admin prefix).
func (w *world) adminCall(method, path, body string, header http.Header) (*reply, error) {
	if w.m != nil {
		return w.managedAdminCall(method, path, body, "", w.m.cred, header)
	}
	if err := w.ensure(); err != nil {
		return nil, err
	}
	r := newRequest(method, w.adminBase()+w.adminPrefix()+w.sub(path))
	if body != "" {
		r.body = []byte(w.sub(body))
		if strings.Contains(path, "/files/") {
			r.header.Set("Content-Type", "application/octet-stream")
		} else {
			r.header.Set("Content-Type", "application/json")
		}
	}
	if w.adminToken != "" && header.Get("Authorization") == "" {
		r.header.Set("Authorization", "Bearer "+w.adminToken)
	}
	for k, vs := range header {
		for _, v := range vs {
			r.header.Add(k, v)
		}
	}
	return w.send(r)
}

// mustAdmin sends an admin request and fails unless the status is one of want.
func (w *world) mustAdmin(method, path, body string, want ...int) (*reply, error) {
	var rep *reply
	var err error
	if w.m != nil {
		rep, err = w.managedAdminCall(method, path, body, "", w.setupCred(w.m.defaultOrg), nil)
	} else {
		rep, err = w.adminCall(method, path, body, nil)
	}
	if err != nil {
		return nil, err
	}
	for _, s := range want {
		if rep.status == s {
			return rep, nil
		}
	}
	return rep, fmt.Errorf("%s %s = %d, want %v; body: %s", method, path, rep.status, want, truncate(rep.body))
}

func truncate(b []byte) string {
	const limit = 600
	if len(b) > limit {
		return string(b[:limit]) + "…"
	}
	return string(b)
}

var dashSegment = regexp.MustCompile(`\.([A-Za-z0-9_]*-[A-Za-z0-9_-]*)`)

// jsonAt evaluates a JSONPath expression against a JSON document.
func jsonAt(doc []byte, path string) ([]any, error) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %w (%s)", err, truncate(doc))
	}
	// Header names such as X-Test contain a dash, which the path parser needs bracketed.
	path = dashSegment.ReplaceAllString(path, `['$1']`)
	x, err := jp.ParseString(path)
	if err != nil {
		return nil, fmt.Errorf("invalid JSONPath %q: %w", path, err)
	}
	return x.Get(v), nil
}

// jsonOne returns the single value at path.
func (w *world) jsonOne(path string) (any, bool, error) {
	if w.last == nil {
		return nil, false, fmt.Errorf("no response yet")
	}
	res, err := jsonAt(w.last.body, w.sub(path))
	if err != nil {
		return nil, false, err
	}
	if len(res) == 0 {
		return nil, false, nil
	}
	return res[0], true, nil
}

// text renders a JSON value as the text a step compares against.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		b, _ := json.Marshal(t) //nolint:errcheck // values come from a parsed document
		return string(b)
	}
}

func queryEscape(s string) string { return url.QueryEscape(s) }

func jsonBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// transportError is a request that failed on the wire: the connection broke, or the response
// was malformed. Faults are meant to cause it, so steps record it instead of failing at once.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }

func (e *transportError) Unwrap() error { return e.err }

// readChunks reads a body, counting the reads that returned data.
func readChunks(r io.Reader) ([]byte, int, error) {
	var out bytes.Buffer
	buf := make([]byte, 32*1024)
	chunks := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunks++
			out.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			return out.Bytes(), chunks, nil
		}
		if err != nil {
			return out.Bytes(), chunks, fmt.Errorf("read body: %w", err)
		}
	}
}
