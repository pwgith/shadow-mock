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

// Write sends the response: it waits for the delay, simulates a fault, or writes the status,
// headers and body (gzipped if the client accepts it). It returns the body actually sent, or
// an error if the client went away during the delay.
func (r *Renderer) Write(ctx context.Context, w http.ResponseWriter, req *http.Request, resp *Response, beforeSend func()) error {
	if r.MaxDelay > 0 {
		resp.Delay = min(resp.Delay, r.MaxDelay)
	}
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
	// Without this Go guesses a Content-Type from the body. WireMock sends none when the stub gives
	// none, and a client that checks the header would see the difference.
	if _, set := h["Content-Type"]; !set {
		h["Content-Type"] = nil
	}
	// WireMock compresses a body of any size when the client accepts gzip, and adds no Vary header.
	if r.Gzip && !resp.GzipDisabled && resp.Dribble == nil && len(body) > 0 && bodyAllowed(resp.Status) && h.Get("Content-Encoding") == "" &&
		acceptsGzip(req.Header.Get("Accept-Encoding")) {
		if z, err := compress(body); err == nil {
			body = z
			h.Set("Content-Encoding", "gzip")
		}
	}
	if resp.StatusMessage != "" && resp.Dribble == nil {
		if sent, err := writeWithReason(w, req, resp.Status, resp.StatusMessage, body); sent || err != nil {
			return err
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

// bodyAllowed reports whether a response with this status may have a body.
func bodyAllowed(status int) bool {
	return status >= 200 && status != http.StatusNoContent && status != http.StatusNotModified
}

// writeWithReason sends the response with a reason phrase of its own. Go's server always writes the
// standard phrase, so the reply is written on the hijacked connection, which is then closed. It
// reports false, having sent nothing, when the connection cannot be taken over (HTTP/2).
func writeWithReason(w http.ResponseWriter, req *http.Request, status int, reason string, body []byte) (bool, error) {
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return false, nil // HTTP/2 and test recorders cannot hijack; the standard phrase is sent
	}
	defer conn.Close()
	h := w.Header().Clone()
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Connection", "close")
	// A line break in the phrase would let a mapping inject headers.
	reason = strings.NewReplacer("\r", " ", "\n", " ").Replace(reason)
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, reason)
	if err := h.Write(&b); err != nil {
		return true, fmt.Errorf("write headers: %w", err)
	}
	b.WriteString("\r\n")
	if req.Method != http.MethodHead {
		b.Write(body)
	}
	if _, err := buf.Write(b.Bytes()); err != nil {
		return true, fmt.Errorf("write response: %w", err)
	}
	if err := buf.Flush(); err != nil {
		return true, fmt.Errorf("write response: %w", err)
	}
	return true, nil
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
	total := time.Duration(d.TotalDuration) * time.Millisecond
	if r.MaxDelay > 0 {
		total = min(total, r.MaxDelay)
	}
	pause := total / time.Duration(n)
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
