package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
)

var b64 = base64.RawURLEncoding

// compact builds a token from a header, claims and a signature function over "header.payload".
func compact(t *testing.T, header, claims any, sign func(signed string) []byte) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b64.EncodeToString(b)
	}
	signed := enc(header) + "." + enc(claims)
	return signed + "." + b64.EncodeToString(sign(signed))
}

func rsaSigner(t *testing.T, key *rsa.PrivateKey) func(string) []byte {
	t.Helper()
	return func(signed string) []byte {
		d := sha256.Sum256([]byte(signed))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
}

func ecSigner(t *testing.T, key *ecdsa.PrivateKey) func(string) []byte {
	t.Helper()
	return func(signed string) []byte {
		d := sha256.Sum256([]byte(signed))
		r, s, err := ecdsa.Sign(rand.Reader, key, d[:])
		if err != nil {
			t.Fatal(err)
		}
		n := (key.Curve.Params().BitSize + 7) / 8
		sig := make([]byte, 2*n)
		r.FillBytes(sig[:n])
		s.FillBytes(sig[n:])
		return sig
	}
}

func parse(t *testing.T, token string) *jwt {
	t.Helper()
	j, err := parseJWT(token)
	if err != nil {
		t.Fatalf("parseJWT: %v", err)
	}
	return j
}

func TestParseJWTRejectsWhatIsNotAToken(t *testing.T) {
	t.Parallel()
	good := b64.EncodeToString([]byte(`{"alg":"RS256"}`))
	body := b64.EncodeToString([]byte(`{"sub":"alice"}`))
	for name, token := range map[string]string{
		"two parts":         good + "." + body,
		"four parts":        good + "." + body + ".x.y",
		"header not base64": "!!!." + body + ".AAAA",
		"body not base64":   good + ".!!!.AAAA",
		"sig not base64":    good + "." + body + ".!!!",
		"header not JSON":   b64.EncodeToString([]byte("nope")) + "." + body + ".AAAA",
		"body not JSON":     good + "." + b64.EncodeToString([]byte("nope")) + ".AAAA",
		"empty":             "",
	} {
		if _, err := parseJWT(token); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestClaimsReadAudienceAsOneOrMany(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		aud  string
		want string
	}{
		"one string": {`"mocks"`, "mocks"},
		"a list":     {`["mocks","other"]`, "mocks,other"},
	} {
		tok := compact(t, map[string]any{"alg": "RS256"}, json.RawMessage(`{"aud":`+tc.aud+`,"exp":1700000000.9}`), func(string) []byte { return []byte{1} })
		j := parse(t, tok)
		if got := strings.Join(j.claims.Audience, ","); got != tc.want {
			t.Errorf("%s: audience = %q, want %q", name, got, tc.want)
		}
		if j.claims.Expires == nil || j.claims.Expires.Unix() != 1700000000 {
			t.Errorf("%s: exp = %v, want seconds read as a whole", name, j.claims.Expires)
		}
	}
	for name, claims := range map[string]string{
		"audience a number": `{"aud":5}`,
		"date a string":     `{"exp":"soon"}`,
	} {
		if _, err := parseJWT(compact(t, map[string]any{"alg": "RS256"}, json.RawMessage(claims), func(string) []byte { return nil })); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerifyPinsTheAlgorithmToTheKeyType(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edSign := func(signed string) []byte { return ed25519.Sign(edKey, []byte(signed)) }
	claims := map[string]any{"sub": "alice"}
	mk := func(alg string, sign func(string) []byte) *jwt {
		return parse(t, compact(t, map[string]any{"alg": alg}, claims, sign))
	}

	tests := []struct {
		name string
		tok  *jwt
		key  crypto.PublicKey
		ok   bool
	}{
		{"RS256 with an RSA key", mk("RS256", rsaSigner(t, rsaKey)), &rsaKey.PublicKey, true},
		{"ES256 with a P-256 key", mk("ES256", ecSigner(t, ecKey)), &ecKey.PublicKey, true},
		{"EdDSA with an Ed25519 key", mk("EdDSA", edSign), edPub, true},
		{"an RSA signature claiming HS256", mk("HS256", rsaSigner(t, rsaKey)), &rsaKey.PublicKey, false},
		{"alg none", mk("none", func(string) []byte { return nil }), &rsaKey.PublicKey, false},
		{"RS256 token, EC key", mk("RS256", rsaSigner(t, rsaKey)), &ecKey.PublicKey, false},
		{"ES256 token, RSA key", mk("ES256", ecSigner(t, ecKey)), &rsaKey.PublicKey, false},
		{"ES256 with the wrong key", mk("ES256", ecSigner(t, p384)), &ecKey.PublicKey, false},
		{"ES256 on a P-384 key", mk("ES256", ecSigner(t, ecKey)), &p384.PublicKey, false},
		{"EdDSA with the wrong key", mk("EdDSA", edSign), ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)), false},
		{"EdDSA token, Ed25519 key, bad alg", mk("ES256", edSign), edPub, false},
		{"RS256 with another RSA key", func() *jwt {
			other, genErr := rsa.GenerateKey(rand.Reader, 2048)
			if genErr != nil {
				t.Fatal(genErr)
			}
			return mk("RS256", rsaSigner(t, other))
		}(), &rsaKey.PublicKey, false},
		{"a key kind the engine does not use", mk("RS256", rsaSigner(t, rsaKey)), "not a key", false},
	}
	for _, tc := range tests {
		if err := tc.tok.verify(tc.key); (err == nil) != tc.ok {
			t.Errorf("%s: verify = %v, want ok=%t", tc.name, err, tc.ok)
		}
	}
}

func TestParsePublicKeyReadsPKIXAndCertificates(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
	if err != nil || !pk.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Errorf("PKIX: %v, %v", pk, err)
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "issuer"}, NotAfter: time.Now().Add(time.Hour)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err = ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})))
	if err != nil || !pk.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Errorf("certificate: %v, %v", pk, err)
	}
	if _, err = ParsePublicKey("not pem"); err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Errorf("text that is not PEM: %v", err)
	}
	if _, err = ParsePublicKey(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("junk")}))); err == nil {
		t.Error("a PEM block that holds no key was accepted")
	}
}

// jwksServer serves a key set that the test can swap, and counts the reads.
type jwksServer struct {
	*httptest.Server
	doc    atomic.Value // string
	status atomic.Int32
	reads  atomic.Int32
}

func newJWKSServer(t *testing.T, doc string) *jwksServer {
	t.Helper()
	s := &jwksServer{}
	s.doc.Store(doc)
	s.status.Store(http.StatusOK)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.reads.Add(1)
		w.WriteHeader(int(s.status.Load()))
		_, _ = w.Write([]byte(s.doc.Load().(string)))
	}))
	t.Cleanup(s.Close)
	return s
}

func jwkSet(t *testing.T, keys ...map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rsaJWK(kid string, k *rsa.PublicKey) map[string]string {
	return map[string]string{"kty": "RSA", "kid": kid, "n": b64.EncodeToString(k.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(k.E)).Bytes())}
}

func TestJWKSReadsEveryKeyType(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ecKey.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	srv := newJWKSServer(t, jwkSet(t,
		rsaJWK("r", &rsaKey.PublicKey),
		map[string]string{"kty": "EC", "kid": "e", "crv": "P-256", "x": b64.EncodeToString(pt[1:33]), "y": b64.EncodeToString(pt[33:])},
		map[string]string{"kty": "OKP", "kid": "o", "crv": "Ed25519", "x": b64.EncodeToString(edPub)},
		map[string]string{"kty": "oct", "kid": "bad-kind"},
		map[string]string{"kty": "RSA", "kid": "bad-rsa", "n": "!!", "e": "AQAB"},
		map[string]string{"kty": "EC", "kid": "bad-ec", "crv": "P-384", "x": "AA", "y": "AA"},
		map[string]string{"kty": "EC", "kid": "off-curve", "crv": "P-256", "x": b64.EncodeToString(make([]byte, 32)), "y": b64.EncodeToString(make([]byte, 32))},
		map[string]string{"kty": "OKP", "kid": "bad-okp", "crv": "X25519", "x": "AA"},
	))
	j := NewJWKS(srv.URL, nil, clock.NewFake(time.Now()))
	ctx := t.Context()

	if k, keyErr := j.Key(ctx, "r"); keyErr != nil || !k.(*rsa.PublicKey).Equal(&rsaKey.PublicKey) {
		t.Errorf("RSA key: %v, %v", k, keyErr)
	}
	if k, keyErr := j.Key(ctx, "e"); keyErr != nil || !k.(*ecdsa.PublicKey).Equal(&ecKey.PublicKey) {
		t.Errorf("EC key: %v, %v", k, keyErr)
	}
	if k, keyErr := j.Key(ctx, "o"); keyErr != nil || !k.(ed25519.PublicKey).Equal(edPub) {
		t.Errorf("OKP key: %v, %v", k, keyErr)
	}
	for _, kid := range []string{"bad-kind", "bad-rsa", "bad-ec", "off-curve", "bad-okp", "missing"} {
		if _, keyErr := j.Key(ctx, kid); keyErr == nil {
			t.Errorf("key %q: found, want it refused", kid)
		}
	}
}

func TestJWKSCachesRereadsOnRotationAndKeepsTheLastSetWhenDown(t *testing.T) {
	t.Parallel()
	k1, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := newJWKSServer(t, jwkSet(t, rsaJWK("k1", &k1.PublicKey)))
	clk := clock.NewFake(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	j := NewJWKS(srv.URL, srv.Client(), clk)
	ctx := t.Context()

	if _, err = j.Key(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Key(ctx, "k1"); err != nil || srv.reads.Load() != 1 {
		t.Fatalf("a fresh set was read %d times (%v), want once", srv.reads.Load(), err)
	}

	// A new key appears: a token naming it is refused until the least gap has passed, so a flood of unknown
	// key ids cannot make the engine hammer the issuer.
	srv.doc.Store(jwkSet(t, rsaJWK("k1", &k1.PublicKey), rsaJWK("k2", &k2.PublicKey)))
	if _, err = j.Key(ctx, "k2"); err == nil || srv.reads.Load() != 1 {
		t.Fatalf("k2 inside the gap: %v after %d reads, want a refusal with no read", err, srv.reads.Load())
	}
	clk.Advance(jwksMinGap + time.Second)
	if k, keyErr := j.Key(ctx, "k2"); keyErr != nil || !k.(*rsa.PublicKey).Equal(&k2.PublicKey) {
		t.Fatalf("k2 after the gap: %v, %v", k, keyErr)
	}

	// The set goes stale and the issuer is down: the last set still serves its keys, but not a key it never had.
	clk.Advance(jwksTTL + time.Second)
	srv.status.Store(http.StatusServiceUnavailable)
	if _, err = j.Key(ctx, "k1"); err != nil {
		t.Errorf("k1 while the issuer is down: %v, want the last set kept", err)
	}
	clk.Advance(jwksMinGap + time.Second)
	if _, err = j.Key(ctx, "never"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("an unknown key while the issuer is down: %v, want the read failure", err)
	}
}

func TestJWKSReportsASetItCannotRead(t *testing.T) {
	t.Parallel()
	notJSON := newJWKSServer(t, "<html>")
	if _, err := NewJWKS(notJSON.URL, nil, clock.NewFake(time.Now())).Key(t.Context(), "k"); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("a set that is not JSON: %v", err)
	}
	if _, err := NewJWKS("http://127.0.0.1:1", nil, clock.NewFake(time.Now())).Key(context.Background(), "k"); err == nil {
		t.Error("an unreachable issuer: no error")
	}
	if _, err := NewJWKS("://bad", nil, clock.NewFake(time.Now())).Key(t.Context(), "k"); err == nil {
		t.Error("a malformed URL: no error")
	}
}

func TestPinnedIgnoresTheKeyID(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Pinned{PublicKey: &key.PublicKey}.Key(t.Context(), "anything")
	if err != nil || got != crypto.PublicKey(&key.PublicKey) {
		t.Errorf("Key = %v, %v", got, err)
	}
}

func TestAPIKeyHasScope(t *testing.T) {
	t.Parallel()
	k := APIKey{Scopes: []string{"sessions", "read"}}
	if !k.Has("read") || k.Has("write") || (APIKey{}).Has("read") {
		t.Error("Has does not report exactly the key's scopes")
	}
}
