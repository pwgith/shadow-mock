package template

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mailgun/raymond/v2"
)

// JWTSettings are the keys the jwt and jwks helpers sign with and publish (FR-10.16). WireMock's
// extension keeps them in the extended global settings; here they are configuration. Whatever is
// empty is made when the engine starts, so tokens are good until the server stops.
type JWTSettings struct {
	// HS256Secret signs tokens made with alg=HS256, the default.
	HS256Secret string
	// KeyID names the RS256 key in the token header and in the key set.
	KeyID string
	// PublicKeyPEM and PrivateKeyPEM are the RS256 key pair. The private key is PKCS #8 or PKCS #1; WireMock labels
	// its PKCS #8 keys "RSA PRIVATE KEY", and the label is not looked at. The public key is
	// worked out from the private key when it is not given.
	PublicKeyPEM  string
	PrivateKeyPEM string
}

// jwtKeys is JWTSettings, read.
type jwtKeys struct {
	secret []byte
	kid    string
	key    *rsa.PrivateKey
}

// EnableJWT adds the jwt, claims, claimsObject and jwks helpers.
func (e *Engine) EnableJWT(s JWTSettings) error {
	keys, err := newJWTKeys(s)
	if err != nil {
		return err
	}
	j := &jwtHelpers{keys: keys, engine: e}
	e.AddHelpers(map[string]any{
		"jwt":          j.jwt,
		"claims":       func(items ...any) any { return list(items) },
		"claimsObject": func(o *raymond.Options) any { return maps.Clone(o.Hash()) },
		"jwks":         j.jwks,
	})
	return nil
}

func newJWTKeys(s JWTSettings) (*jwtKeys, error) {
	k := &jwtKeys{secret: []byte(s.HS256Secret), kid: s.KeyID}
	var err error
	if len(k.secret) == 0 {
		if k.secret, err = randomAlphanumeric(36); err != nil {
			return nil, err
		}
	}
	if k.kid == "" {
		id, idErr := randomAlphanumeric(30)
		if idErr != nil {
			return nil, idErr
		}
		k.kid = string(id)
	}
	if s.PrivateKeyPEM == "" {
		if k.key, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			return nil, fmt.Errorf("make an RS256 key: %w", err)
		}
		return k, nil
	}
	if k.key, err = parsePrivateKey(s.PrivateKeyPEM); err != nil {
		return nil, fmt.Errorf("templating.jwt.rs256PrivateKey: %w", err)
	}
	if s.PublicKeyPEM != "" {
		pub, pubErr := parsePublicKey(s.PublicKeyPEM)
		if pubErr != nil {
			return nil, fmt.Errorf("templating.jwt.rs256PublicKey: %w", pubErr)
		}
		if pub.N.Cmp(k.key.N) != 0 || pub.E != k.key.E {
			return nil, errors.New("templating.jwt: the RS256 public key is not the public half of the private key")
		}
	}
	return k, nil
}

func randomAlphanumeric(n int) ([]byte, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return nil, fmt.Errorf("make a secret: %w", err)
		}
		out[i] = alphabet[v.Int64()]
	}
	return out, nil
}

// pemBody returns the DER bytes in a PEM block, whatever its label (WireMock's labels are not the standard ones).
func pemBody(text string) ([]byte, error) {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "-----") {
			b.WriteString(line)
		}
	}
	der, err := base64.StdEncoding.DecodeString(b.String())
	if err != nil {
		if blk, _ := pem.Decode([]byte(text)); blk != nil {
			return blk.Bytes, nil
		}
		return nil, errors.New("not PEM")
	}
	return der, nil
}

func parsePrivateKey(text string) (*rsa.PrivateKey, error) {
	der, err := pemBody(text)
	if err != nil {
		return nil, err
	}
	if k, pkcs8Err := x509.ParsePKCS8PrivateKey(der); pkcs8Err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("the key is not an RSA key")
	}
	k, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, errors.New("not a PKCS #8 or PKCS #1 RSA private key")
	}
	return k, nil
}

func parsePublicKey(text string) (*rsa.PublicKey, error) {
	der, err := pemBody(text)
	if err != nil {
		return nil, err
	}
	if k, pkixErr := x509.ParsePKIXPublicKey(der); pkixErr == nil {
		if rk, ok := k.(*rsa.PublicKey); ok {
			return rk, nil
		}
		return nil, errors.New("the key is not an RSA key")
	}
	k, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, errors.New("not an X.509 or PKCS #1 RSA public key")
	}
	return k, nil
}

type jwtHelpers struct {
	keys   *jwtKeys
	engine *Engine
}

// algRS256 is the algorithm that signs with the RSA key; the default, HS256, signs with the secret.
const algRS256 = "RS256"

// reservedClaim reports whether the jwt helper reads a hash entry itself; every other entry is a claim of its own.
func reservedClaim(key string) bool {
	switch key {
	case "exp", "iss", "aud", "sub", "nbf":
		return true
	}
	return false
}

// jwt implements {{jwt maxAge='1 hour' iss='me' alg='RS256' role='admin'}}, as WireMock's JWT extension does.
// A mistake in the arguments is written in the response as [ERROR: …], as WireMock's helpers do.
func (j *jwtHelpers) jwt(o *raymond.Options) any {
	token, err := j.token(o.Hash())
	if err != nil {
		return safe("[ERROR: " + err.Error() + "]")
	}
	return safe(token)
}

func (j *jwtHelpers) token(hash map[string]any) (string, error) {
	now := j.engine.clock.Now()
	expiry, err := expiryOf(hash, now)
	if err != nil {
		return "", err
	}
	claims := map[string]any{
		"exp": expiry.Unix(),
		"iat": now.Unix(),
		"iss": stringOr(hash, "iss", "wiremock"),
		"aud": stringOr(hash, "aud", "wiremock.io"),
		"sub": stringOr(hash, "sub", "user-123"),
	}
	if v, ok := hash["nbf"]; ok {
		nbf, nbfErr := asTime(v)
		if nbfErr != nil {
			return "", fmt.Errorf("nbf: %w", nbfErr)
		}
		claims["nbf"] = nbf.Unix()
	}
	for key, v := range hash {
		if reservedClaim(key) {
			continue
		}
		c, ok, claimErr := claimValue(key, v)
		if claimErr != nil {
			return "", claimErr
		}
		if ok {
			claims[key] = c
		}
	}
	alg := "HS256"
	if v, ok := hash["alg"]; ok {
		alg = text(v)
	}
	header := map[string]any{"typ": "JWT", "alg": "HS256"}
	if alg == algRS256 {
		header["alg"], header["kid"] = algRS256, j.keys.kid
	}
	return j.sign(header, claims, alg == algRS256)
}

func (j *jwtHelpers) sign(header, claims map[string]any, rs256 bool) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	var sig []byte
	if rs256 {
		digest := sha256.Sum256([]byte(input))
		if sig, err = rsa.SignPKCS1v15(rand.Reader, j.keys.key, crypto.SHA256, digest[:]); err != nil {
			return "", fmt.Errorf("sign: %w", err)
		}
	} else {
		mac := hmac.New(sha256.New, j.keys.secret)
		mac.Write([]byte(input))
		sig = mac.Sum(nil)
	}
	return input + "." + enc.EncodeToString(sig), nil
}

// expiryOf is exp when given, and otherwise now plus maxAge, which is 100 years when not given.
func expiryOf(hash map[string]any, now time.Time) (time.Time, error) {
	if v, ok := hash["exp"]; ok {
		t, err := asTime(v)
		if err != nil {
			return time.Time{}, fmt.Errorf("exp: %w", err)
		}
		return t, nil
	}
	age, err := parseMaxAge(hash["maxAge"])
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(age), nil
}

func parseMaxAge(v any) (time.Duration, error) {
	if v == nil {
		return 36500 * 24 * time.Hour, nil
	}
	parts := strings.Split(text(v), " ")
	if len(parts) != 2 {
		return 0, errors.New("maxAge must consist of two parts - amount and unit e.g. 12 days")
	}
	amount, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("maxAge amount must be a whole number")
	}
	units := map[string]time.Duration{
		"NANOS": time.Nanosecond, "MICROS": time.Microsecond, "MILLIS": time.Millisecond, "SECONDS": time.Second,
		"MINUTES": time.Minute, "HOURS": time.Hour, "HALF_DAYS": 12 * time.Hour, "DAYS": 24 * time.Hour,
	}
	unit, ok := units[strings.ToUpper(parts[1])]
	if !ok {
		return 0, errors.New("maxAge unit must be one of: seconds, minutes, hours, days")
	}
	return time.Duration(amount) * unit, nil
}

// asTime reads a date a template wrote: an ISO 8601 date such as the now helper gives.
func asTime(v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	return parseTime(text(v), "")
}

func stringOr(hash map[string]any, key, fallback string) string {
	if v, ok := hash[key]; ok {
		return text(v)
	}
	return fallback
}

// claimValue converts a hash entry to a claim. A value of a kind a claim cannot be is left out, as in WireMock.
func claimValue(key string, v any) (value any, ok bool, err error) {
	switch t := v.(type) {
	case bool:
		return t, true, nil
	case string:
		return t, true, nil
	case raymond.SafeString:
		return string(t), true, nil
	case map[string]any:
		return t, true, nil
	case list:
		arr, arrErr := arrayClaim(key, t)
		return arr, arrErr == nil, arrErr
	case []any:
		arr, arrErr := arrayClaim(key, t)
		return arr, arrErr == nil, arrErr
	}
	switch rv := reflect.ValueOf(v); rv.Kind() { //nolint:exhaustive // every other kind is left out of the claims
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true, nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true, nil
	}
	return nil, false, nil
}

// arrayClaim checks a list is all strings or all whole numbers, which is all the extension allows.
func arrayClaim(key string, items []any) (any, error) {
	if len(items) == 0 {
		return []string{}, nil
	}
	first := reflect.TypeOf(items[0])
	for _, it := range items {
		if reflect.TypeOf(it) != first {
			return nil, fmt.Errorf("items for array claim %s are not all the same type", key)
		}
	}
	out := make([]any, len(items))
	for i, it := range items {
		switch t := it.(type) {
		case string:
			out[i] = t
		case raymond.SafeString:
			out[i] = string(t)
		case int, int32, int64:
			out[i] = reflect.ValueOf(t).Int()
		default:
			return nil, fmt.Errorf("items for array claim %s are not of type string or integer", key)
		}
	}
	return out, nil
}

// jwks implements {{jwks}}: the JSON Web Key Set that holds the public half of the RS256 key.
func (j *jwtHelpers) jwks(_ *raymond.Options) any {
	pub := j.keys.key.PublicKey
	enc := base64.RawURLEncoding
	e := big.NewInt(int64(pub.E)).Bytes()
	key := struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	}{"RSA", j.keys.kid, "sig", algRS256, enc.EncodeToString(pub.N.Bytes()), enc.EncodeToString(e)}
	b, err := json.Marshal(map[string]any{"keys": []any{key}})
	if err != nil {
		return safe("[ERROR: " + err.Error() + "]")
	}
	return safe(string(b))
}
