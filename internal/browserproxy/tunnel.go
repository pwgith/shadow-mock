package browserproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Proxy answers CONNECT requests by ending the client's TLS with a certificate from the authority
// and serving the requests inside the tunnel with a handler.
type Proxy struct {
	CA     *CA
	Logger *slog.Logger
	// WriteTimeout bounds one response inside a tunnel, as it does on the listener.
	WriteTimeout time.Duration
}

type tunnelKey struct{}

// Authority returns the host:port the client asked CONNECT to reach, for a request that arrived inside
// a tunnel.
func Authority(ctx context.Context) (string, bool) {
	a, ok := ctx.Value(tunnelKey{}).(string)
	return a, ok
}

// hostName is what a CONNECT target may be called: letters, digits, dots, hyphens and underscores.
var hostName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)

// target splits and checks the host:port of a CONNECT request.
func target(authority string) (host string, ok bool) {
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		return "", false
	}
	if port, convErr := strconv.Atoi(p); convErr != nil || port < 1 || port > 65535 {
		return "", false
	}
	if net.ParseIP(h) != nil || hostName.MatchString(h) {
		return h, true
	}
	return "", false
}

// Serve takes over the connection of a CONNECT request: it says the tunnel is open, ends the TLS, and
// serves the requests in it with next until the client closes the connection.
func (p *Proxy) Serve(w http.ResponseWriter, r *http.Request, next http.Handler) {
	host, ok := target(r.Host)
	if !ok {
		http.Error(w, "CONNECT needs a host:port to reach", http.StatusBadRequest)
		return
	}
	cert, err := p.CA.Leaf(host)
	if err != nil {
		p.Logger.WarnContext(r.Context(), "browser proxy: no certificate", "host", host, "error", err.Error())
		http.Error(w, "no certificate could be made for "+host, http.StatusInternalServerError)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT needs HTTP/1.1", http.StatusNotImplemented)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		p.Logger.WarnContext(r.Context(), "browser proxy: cannot take the connection", "error", err.Error())
		return
	}
	if buf.Reader.Buffered() > 0 { // a client sends nothing until it has the answer
		_ = conn.Close()
		return
	}
	// WireMock answers with a bare 200.
	if _, err := conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")); err != nil {
		_ = conn.Close()
		return
	}
	p.tunnel(r.Context(), conn, cert, r.Host, next)
}

func (p *Proxy) tunnel(ctx context.Context, conn net.Conn, cert *tls.Certificate, authority string, next http.Handler) {
	tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	hsCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(hsCtx); err != nil {
		p.Logger.DebugContext(ctx, "browser proxy: the client did not accept the certificate", "host", authority, "error", err.Error())
		_ = conn.Close()
		return
	}
	state := tc.ConnectionState()
	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.Clone(context.WithValue(req.Context(), tunnelKey{}, authority))
		req.Host, req.TLS = authority, &state
		next.ServeHTTP(w, req)
	})
	ln := newSingleConn(tc)
	srv := &http.Server{
		Handler:           inner,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      p.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		p.Logger.DebugContext(ctx, "browser proxy: tunnel ended", "host", authority, "error", err.Error())
	}
}

// singleConn is a listener that hands out one connection and reports closed when that connection is.
type singleConn struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newSingleConn(c net.Conn) *singleConn {
	l := &singleConn{ch: make(chan net.Conn, 1), done: make(chan struct{})}
	l.ch <- &notifyConn{Conn: c, closed: l.shut}
	return l
}

// Accept returns the connection once, and then waits for it to close.
func (l *singleConn) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *singleConn) shut() { l.once.Do(func() { close(l.done) }) }

// Close ends the listener.
func (l *singleConn) Close() error {
	l.shut()
	return nil
}

// Addr is of no use: the connection already exists.
func (l *singleConn) Addr() net.Addr { return &net.TCPAddr{} }

// notifyConn tells its listener when it is closed, which ends the server that was given the listener.
type notifyConn struct {
	net.Conn
	closed func()
}

// Close closes the connection and ends the listener that handed it out.
func (c *notifyConn) Close() error {
	err := c.Conn.Close()
	c.closed()
	return err
}
