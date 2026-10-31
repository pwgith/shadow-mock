package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	amzDate     = "20060102T150405Z"
	amzDay      = "20060102"
	algorithm   = "AWS4-HMAC-SHA256"
	serviceName = "s3"
)

// sign adds the SigV4 headers to req. payloadHash is the hex SHA-256 of the body. Only host, x-amz-content-sha256,
// x-amz-date and x-amz-security-token are signed, and range when there is one.
func (c *Client) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	req.Header.Set("X-Amz-Date", now.Format(amzDate))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if c.cfg.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.cfg.SessionToken)
	}
	names := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if c.cfg.SessionToken != "" {
		names = append(names, "x-amz-security-token")
	}
	if req.Header.Get("Range") != "" {
		names = append(names, "range")
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		v := req.Header.Get(n)
		if n == "host" {
			v = req.URL.Host
		}
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(v) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := strings.Join([]string{now.Format(amzDay), c.cfg.Region, serviceName, "aws4_request"}, "/")
	digest := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{algorithm, now.Format(amzDate), scope, hex.EncodeToString(digest[:])}, "\n")

	key := hmacSHA256([]byte("AWS4"+c.cfg.SecretKey), now.Format(amzDay))
	key = hmacSHA256(key, c.cfg.Region)
	key = hmacSHA256(key, serviceName)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", algorithm+" Credential="+c.cfg.AccessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+signature)
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// canonicalQuery sorts the query by name and encodes it the way SigV4 asks.
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, escape(k)+"="+escape(v))
		}
	}
	return strings.Join(parts, "&")
}

// escape percent-encodes everything except the unreserved characters, as SigV4 requires.
func escape(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		}
	}
	return b.String()
}

// escapePath is escape for an object key: the slashes between segments stay.
func escapePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = escape(p)
	}
	return strings.Join(parts, "/")
}
