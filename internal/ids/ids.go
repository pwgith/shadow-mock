// Package ids generates identifiers and secrets from crypto/rand (NFR-5.1, §10).
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

// Crockford's base32 alphabet, used for session keys and key-based session ids.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Prefixes for generated identifiers.
const (
	SessionKeyPrefix = "smk_"
	SessionIDPrefix  = "ses_"
)

// DefaultURLMockIDLength is the default URL-based mock id length: 16 characters, 80 bits.
const DefaultURLMockIDLength = 16

// lowerBase32 is RFC 4648 base32 in lower case, without padding: URL-safe and unambiguous.
func lowerBase32() *base32.Encoding {
	return base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
}

func crockfordEncoding() *base32.Encoding {
	return base32.NewEncoding(crockford).WithPadding(base32.NoPadding)
}

func random(n int) []byte {
	b := make([]byte, n)
	// crypto/rand.Read never returns an error on supported platforms (Go 1.24+).
	_, _ = rand.Read(b)
	return b
}

// token returns 26 Crockford base32 characters (130 bits, of which 128 are random).
func token() string {
	return crockfordEncoding().EncodeToString(random(16))[:26]
}

// NewSessionKey returns a session key: "smk_" plus 26 characters, 128 bits of entropy.
func NewSessionKey(prefix string) string {
	if prefix == "" {
		prefix = SessionKeyPrefix
	}
	return prefix + token()
}

// NewSessionID returns a key-based session id: "ses_" plus 26 characters.
func NewSessionID() string { return SessionIDPrefix + token() }

// NewURLMockID returns a lower-case base32 URL-based mock id of the given length.
func NewURLMockID(length int) string {
	if length <= 0 {
		length = DefaultURLMockIDLength
	}
	s := lowerBase32().EncodeToString(random((length*5 + 7) / 8))
	return s[:length]
}

// NewUUID returns a random (version 4) UUID.
func NewUUID() string {
	b := random(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return strings.Join([]string{h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]}, "-")
}

// IsSessionID reports whether s has the shape of a key-based session id or a URL-based mock id.
func IsSessionID(s string) bool {
	if rest, ok := strings.CutPrefix(s, SessionIDPrefix); ok {
		return len(rest) == 26 && strings.Trim(rest, crockford) == ""
	}
	return len(s) == DefaultURLMockIDLength && strings.Trim(s, "abcdefghijklmnopqrstuvwxyz234567") == ""
}
