// Package s3 is a blob.Store over the S3 API: AWS S3, Cloudflare R2, MinIO, Backblaze B2, Supabase Storage and
// anything else that speaks it. It signs requests with SigV4 itself, so the engine takes no cloud SDK.
package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/clock"
)

// Config is how to reach a bucket.
type Config struct {
	// Endpoint is the base URL of the store, such as https://<account>.r2.cloudflarestorage.com. Empty means AWS.
	Endpoint string
	Region   string
	Bucket   string
	// AccessKey and SecretKey sign the requests. SessionToken is for temporary credentials and may be empty.
	AccessKey    string
	SecretKey    string
	SessionToken string
	// PathStyle puts the bucket in the path (host/bucket/key). Without it the bucket goes in the host name
	// (bucket.host/key). Stores reached by an IP address or a local name need it.
	PathStyle bool
	// Timeout bounds each request. Zero means 10 seconds.
	Timeout time.Duration
}

// maxErrorBody bounds how much of an error answer is read.
const maxErrorBody = 4 << 10

// Client is a blob.Store over one bucket.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client
	clk  clock.Clock
}

var _ blob.Store = (*Client)(nil)

// New checks the configuration and returns a client. It makes no request.
func New(cfg Config, clk clock.Clock) (*Client, error) {
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: bucket, access key and secret key are needed")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	base, err := url.Parse(cfg.Endpoint)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		return nil, fmt.Errorf("s3: endpoint %q must be an http or https URL", cfg.Endpoint)
	}
	return &Client{cfg: cfg, base: base, http: &http.Client{Timeout: cfg.Timeout}, clk: clk}, nil
}

// target builds the URL of a key, or of the bucket when key is "".
func (c *Client) target(key string, query url.Values) *url.URL {
	u := *c.base
	host, path := c.base.Host, ""
	if c.cfg.PathStyle {
		path = "/" + c.cfg.Bucket
	} else {
		host = c.cfg.Bucket + "." + host
	}
	if key != "" {
		path += "/" + escapePath(key)
	} else if path == "" {
		path = "/"
	}
	u.Host = host
	u.Path, _ = url.PathUnescape(path) //nolint:errcheck // path is built from escaped parts, so it unescapes
	u.RawPath = path
	u.RawQuery = query.Encode()
	return &u
}

// do sends one signed request and returns the response, which the caller closes. A network failure or a server
// error is blob.ErrUnavailable; every answer below 500 comes back to the caller to read.
func (c *Client) do(ctx context.Context, method string, u *url.URL, body []byte, header http.Header) (*http.Response, error) {
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("s3: build request: %w", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	sum := sha256.Sum256(body)
	c.sign(req, hex.EncodeToString(sum[:]), c.clk.Now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 %s %s: %w: %w", method, u.Path, blob.ErrUnavailable, err)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		defer resp.Body.Close()
		return nil, fmt.Errorf("s3 %s %s: %w: %s", method, u.Path, blob.ErrUnavailable, describe(resp))
	}
	return resp, nil
}

// describe summarises an error answer: the status and the store's error code.
func describe(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody)) //nolint:errcheck // a partial body is enough to describe the error
	var e struct {
		Code string `xml:"Code"`
	}
	if xml.Unmarshal(b, &e) == nil && e.Code != "" {
		return resp.Status + " (" + e.Code + ")"
	}
	return resp.Status
}

// fail turns an unexpected answer into an error.
func fail(method, key string, resp *http.Response) error {
	defer resp.Body.Close()
	return fmt.Errorf("s3 %s %s: %s", method, key, describe(resp))
}

// Put implements blob.Store.
func (c *Client) Put(ctx context.Context, key string, data []byte, meta blob.Meta) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	h := http.Header{}
	if meta.ContentType != "" {
		h.Set("Content-Type", meta.ContentType)
	}
	resp, err := c.do(ctx, http.MethodPut, c.target(key, nil), data, h)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fail("PUT", key, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // draining so the connection can be reused
	return resp.Body.Close()
}

// Get implements blob.Store.
func (c *Client) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, error) {
	if err := blob.ValidKey(key); err != nil {
		return nil, err
	}
	h := http.Header{}
	if rng != nil {
		spec := "bytes=" + strconv.FormatInt(rng.Off, 10) + "-"
		if rng.Len > 0 {
			spec += strconv.FormatInt(rng.Off+rng.Len-1, 10)
		}
		h.Set("Range", spec)
	}
	resp, err := c.do(ctx, http.MethodGet, c.target(key, nil), nil, h)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", key, blob.ErrNotFound)
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = resp.Body.Close()
		return io.NopCloser(bytes.NewReader(nil)), nil
	case resp.StatusCode/100 != 2:
		return nil, fail("GET", key, resp)
	}
	return resp.Body, nil
}

// Stat implements blob.Store.
func (c *Client) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := blob.ValidKey(key); err != nil {
		return blob.Info{}, err
	}
	resp, err := c.do(ctx, http.MethodHead, c.target(key, nil), nil, nil)
	if err != nil {
		return blob.Info{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return blob.Info{}, fmt.Errorf("%s: %w", key, blob.ErrNotFound)
	case resp.StatusCode/100 != 2:
		return blob.Info{}, fmt.Errorf("s3 HEAD %s: %s", key, resp.Status)
	}
	return blob.Info{Size: resp.ContentLength, ContentType: resp.Header.Get("Content-Type")}, nil
}

// Delete implements blob.Store.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := blob.ValidKey(key); err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodDelete, c.target(key, nil), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fail("DELETE", key, resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // draining so the connection can be reused
	return resp.Body.Close()
}

type listResult struct {
	Contents []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	Truncated bool   `xml:"IsTruncated"`
	NextToken string `xml:"NextContinuationToken"`
}

// List implements blob.Store.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	if err := blob.ValidPrefix(prefix); err != nil {
		return nil, err
	}
	out := []string{}
	token := ""
	for {
		q := map[string][]string{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q["continuation-token"] = []string{token}
		}
		resp, err := c.do(ctx, http.MethodGet, c.target("", q), nil, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			return nil, fail("LIST", prefix, resp)
		}
		var res listResult
		err = xml.NewDecoder(resp.Body).Decode(&res)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("s3 LIST %s: %w: %w", prefix, blob.ErrUnavailable, err)
		}
		for _, o := range res.Contents {
			if strings.HasPrefix(o.Key, prefix) {
				out = append(out, o.Key)
			}
		}
		if !res.Truncated || res.NextToken == "" {
			return out, nil
		}
		token = res.NextToken
	}
}
