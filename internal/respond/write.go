package respond

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/model"
)

// gzipMinBytes is the smallest body worth compressing (DESIGN.md §9).
const gzipMinBytes = 1024

// Write sends the response: it waits for the delay, simulates a fault, or writes the status,
// headers and body (gzipped if the client accepts it). It returns the body actually sent, or
// an error if the client went away during the delay.
func (r *Renderer) Write(ctx context.Context, w http.ResponseWriter, req *http.Request, resp *Response, beforeSend func()) error {
	if resp.Delay > 0 && !r.Clock.Sleep(ctx, resp.Delay) {
		return fmt.Errorf("delay: %w", ctx.Err())
	}
	// The caller records the request before the client can see the response (FR-5.5).
	if beforeSend != nil {
		beforeSend()
	}
	if resp.Fault != "" {
		return fault(w, resp.Fault)
	}
	body := resp.Body
	h := w.Header()
	for k, vs := range resp.Header {
		h[k] = vs
	}
	if r.Gzip && !resp.GzipDisabled && len(body) >= gzipMinBytes && h.Get("Content-Encoding") == "" &&
		acceptsGzip(req.Header.Get("Accept-Encoding")) {
		if z, err := compress(body); err == nil {
			body = z
			h.Set("Content-Encoding", "gzip")
			h.Add("Vary", "Accept-Encoding")
		}
	}
	if resp.Dribble != nil && resp.Dribble.NumberOfChunks > 0 {
		return r.dribble(ctx, w, resp.Status, body, resp.Dribble)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.Status)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip: it names gzip with a
// quality above zero (gzip;q=0 refuses it, and gzip;q=0.8 allows it).
func acceptsGzip(ae string) bool {
	for _, part := range strings.Split(ae, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		q := 1.0
		if name, value, found := strings.Cut(strings.ReplaceAll(params, " ", ""), "="); found && strings.EqualFold(name, "q") {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				q = parsed
			}
		}
		return q > 0
	}
	return false
}

func compress(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// dribble sends the body in chunks spread over the total duration (FR-3.2).
func (r *Renderer) dribble(ctx context.Context, w http.ResponseWriter, status int, body []byte, d *model.ChunkedDribbleDelay) error {
	w.WriteHeader(status)
	n := d.NumberOfChunks
	size := (len(body) + n - 1) / max(n, 1)
	pause := time.Duration(d.TotalDuration) * time.Millisecond / time.Duration(n)
	flusher, canFlush := w.(http.Flusher)
	for i := 0; i < len(body); i += max(size, 1) {
		if !r.Clock.Sleep(ctx, pause) { // every chunk waits, so the body takes the whole duration
			return fmt.Errorf("dribble: %w", ctx.Err())
		}
		end := min(i+max(size, 1), len(body))
		if _, err := w.Write(body[i:end]); err != nil {
			return fmt.Errorf("dribble: %w", err)
		}
		if canFlush {
			flusher.Flush()
		}
	}
	return nil
}

// fault simulates a broken connection (FR-3.3).
func fault(w http.ResponseWriter, kind string) error {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return fmt.Errorf("fault %s: the connection cannot be hijacked", kind)
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return fmt.Errorf("fault %s: %w", kind, err)
	}
	defer conn.Close()
	switch kind {
	case model.FaultConnectionReset:
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0) //nolint:errcheck // best effort: a zero linger sends RST on close
		}
	case model.FaultMalformedChunk:
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nlorem ipsum garbage\r\n") //nolint:errcheck // deliberately broken
		_ = buf.Flush()
	case model.FaultRandomDataThenClose:
		junk := make([]byte, 64)
		_, _ = rand.Read(junk)
		_, _ = buf.Write(junk) //nolint:errcheck // deliberately broken
		_ = buf.Flush()
	}
	return nil
}
