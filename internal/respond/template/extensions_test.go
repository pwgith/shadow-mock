package template_test

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
	"io"
	"math/big"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/respond/template"
)

var epoch = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// javaPEM writes a key the way WireMock's JWT extension does: standard encodings under the labels
// "RSA PRIVATE KEY" and "RSA PUBLIC KEY" that Java's does not match to them.
func javaPEM(t *testing.T, key *rsa.PrivateKey) (public, private string) {
	t.Helper()
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(label string, der []byte) string {
		return "-----BEGIN " + label + "-----\n" + base64.StdEncoding.EncodeToString(der) + "\n-----END " + label + "-----\n"
	}
	return wrap("RSA PUBLIC KEY", pub), wrap("RSA PRIVATE KEY", priv)
}

func newJWT(t *testing.T, s template.JWTSettings) *template.Engine {
	t.Helper()
	e := template.New(clock.NewFake(epoch))
	if err := e.EnableJWT(s); err != nil {
		t.Fatal(err)
	}
	return e
}

func render(t *testing.T, e *template.Engine, src string) string {
	t.Helper()
	out, err := e.Render(src, map[string]any{})
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return out
}

// decode splits a token and decodes its header and claims.
func decode(t *testing.T, token string) (header, claims map[string]any, signed string, sig []byte) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("want three parts, got %q", token)
	}
	dec := func(s string) map[string]any {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		return m
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	return dec(parts[0]), dec(parts[1]), parts[0] + "." + parts[1], sig
}

func TestJWTDefaultsAreWireMocks(t *testing.T) {
	t.Parallel()
	e := newJWT(t, template.JWTSettings{HS256Secret: "s3cret"})
	header, claims, signed, sig := decode(t, render(t, e, `{{jwt}}`))
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Errorf("header = %v", header)
	}
	want := map[string]any{
		"iss": "wiremock", "aud": "wiremock.io", "sub": "user-123",
		"iat": float64(epoch.Unix()), "exp": float64(epoch.Add(36500 * 24 * time.Hour).Unix()),
	}
	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %s = %v, want %v", k, claims[k], v)
		}
	}
	if len(claims) != len(want) {
		t.Errorf("claims = %v, want only %v", claims, want)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(signed))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		t.Error("the HS256 signature does not verify with the secret")
	}
}

func TestJWTClaims(t *testing.T) {
	t.Parallel()
	e := newJWT(t, template.JWTSettings{HS256Secret: "k"})
	token := render(t, e, `{{jwt maxAge='2 hours' iss='me' aud='you' sub='them' nbf=(now offset='-1 hours') admin=true level=3 ratio=1.5 role='ops' tags=(claims 'a' 'b') nums=(claims 1 2) profile=(claimsObject name='x' n=2)}}`)
	_, claims, _, _ := decode(t, token)
	checks := map[string]any{
		"exp": float64(epoch.Add(2 * time.Hour).Unix()), "iss": "me", "aud": "you", "sub": "them",
		"nbf": float64(epoch.Add(-time.Hour).Unix()), "admin": true, "level": float64(3), "ratio": 1.5, "role": "ops",
	}
	for k, v := range checks {
		if claims[k] != v {
			t.Errorf("claim %s = %v (%T), want %v", k, claims[k], claims[k], v)
		}
	}
	if got, _ := json.Marshal(claims["tags"]); string(got) != `["a","b"]` {
		t.Errorf("tags = %s", got)
	}
	if got, _ := json.Marshal(claims["nums"]); string(got) != `[1,2]` {
		t.Errorf("nums = %s", got)
	}
	if got, _ := json.Marshal(claims["profile"]); string(got) != `{"n":2,"name":"x"}` {
		t.Errorf("profile = %s", got)
	}
}

func TestJWTExpiryFromADate(t *testing.T) {
	t.Parallel()
	e := newJWT(t, template.JWTSettings{})
	_, claims, _, _ := decode(t, render(t, e, `{{jwt exp=(now offset='3 days')}}`))
	if want := float64(epoch.Add(72 * time.Hour).Unix()); claims["exp"] != want {
		t.Errorf("exp = %v, want %v", claims["exp"], want)
	}
}

func TestJWTWithRS256(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(testRand(), 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv := javaPEM(t, key)
	e := newJWT(t, template.JWTSettings{KeyID: "kid-1", PublicKeyPEM: pub, PrivateKeyPEM: priv})
	header, _, signed, sig := decode(t, render(t, e, `{{jwt alg='RS256'}}`))
	if header["alg"] != "RS256" || header["kid"] != "kid-1" {
		t.Errorf("header = %v", header)
	}
	digest := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the RS256 signature does not verify with the public key: %v", err)
	}

	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(render(t, e, `{{jwks}}`)), &set); err != nil || len(set.Keys) != 1 {
		t.Fatalf("jwks = %v, %v", set, err)
	}
	k := set.Keys[0]
	n, _ := base64.RawURLEncoding.DecodeString(k.N)
	ex, _ := base64.RawURLEncoding.DecodeString(k.E)
	if k.Kty != "RSA" || k.Kid != "kid-1" || k.Use != "sig" || k.Alg != "RS256" ||
		new(big.Int).SetBytes(n).Cmp(key.N) != 0 || new(big.Int).SetBytes(ex).Int64() != int64(key.E) {
		t.Errorf("the key set does not describe the key: %+v", k)
	}
}

func TestJWTKeysAreMadeWhenNotGiven(t *testing.T) {
	t.Parallel()
	e := newJWT(t, template.JWTSettings{})
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`).MatchString(render(t, e, `{{jwt alg='RS256'}}`)) {
		t.Error("no RS256 token")
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(render(t, e, `{{jwks}}`)), &set); err != nil || len(set.Keys) != 1 || len(set.Keys[0].Kid) != 30 {
		t.Errorf("a key id of 30 characters was expected: %+v, %v", set, err)
	}
}

func TestJWTKeysAreChecked(t *testing.T) {
	t.Parallel()
	key, _ := rsa.GenerateKey(testRand(), 2048)
	other, _ := rsa.GenerateKey(testRand(), 2048)
	_, priv := javaPEM(t, key)
	otherPub, _ := javaPEM(t, other)
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	e := template.New(clock.Real{})
	for name, tt := range map[string]struct {
		s    template.JWTSettings
		want string
	}{
		"a PKCS #1 private key":             {template.JWTSettings{PrivateKeyPEM: pkcs1}, ""},
		"not PEM":                           {template.JWTSettings{PrivateKeyPEM: "nope"}, "rs256PrivateKey"},
		"a public key that is not the pair": {template.JWTSettings{PrivateKeyPEM: priv, PublicKeyPEM: otherPub}, "not the public half"},
		"a bad public key":                  {template.JWTSettings{PrivateKeyPEM: priv, PublicKeyPEM: "nope"}, "rs256PublicKey"},
	} {
		err := e.EnableJWT(tt.s)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: want an error containing %q, got %v", name, tt.want, err)
		}
	}
}

func TestJWTMistakesAreWrittenIntoTheResponse(t *testing.T) {
	t.Parallel()
	e := newJWT(t, template.JWTSettings{})
	for src, want := range map[string]string{
		`{{jwt maxAge='12'}}`:                   "[ERROR: maxAge must consist of two parts - amount and unit e.g. 12 days]",
		`{{jwt maxAge='x days'}}`:               "[ERROR: maxAge amount must be a whole number]",
		`{{jwt maxAge='1 fortnights'}}`:         "[ERROR: maxAge unit must be one of: seconds, minutes, hours, days]",
		`{{jwt tags=(claims 'a' 1)}}`:           "[ERROR: items for array claim tags are not all the same type]",
		`{{jwt tags=(claims 1.5 2.5)}}`:         "[ERROR: items for array claim tags are not of type string or integer]",
		`{{jwt exp='not a date'}}`:              "[ERROR: exp: ",
		`{{jwt maxAge='1 SECONDS' nbf='nope'}}`: "[ERROR: nbf: ",
	} {
		if got := render(t, e, src); !strings.HasPrefix(got, want) {
			t.Errorf("%s = %q, want it to start %q", src, got, want)
		}
	}
}

func TestJWTHelpersAreOffUntilEnabled(t *testing.T) {
	t.Parallel()
	e := template.New(clock.Real{})
	if _, err := e.Render(`{{jwt maxAge='1 hour'}}`, map[string]any{}); err == nil {
		t.Error("the jwt helper must not exist until the extension is on")
	}
}

func TestFakerExpressions(t *testing.T) {
	t.Parallel()
	e := template.New(clock.Real{})
	e.EnableFaker()
	shapes := map[string]string{
		"Name.firstName":               `^[A-Z][a-z]+$`,
		"Name.first_name":              `^[A-Z][a-z]+$`,
		"name.lastName":                `^[A-Z][a-z]+$`,
		"Name.fullName":                `^\S+ \S+$`,
		"Name.username":                `^[a-z]+\.[a-z]+$`,
		"Address.city":                 `^[A-Z][a-z]+$`,
		"Address.streetAddress":        `^\d+ .+ \w+$`,
		"Address.zipCode":              `^\d{5}$`,
		"Address.stateAbbr":            `^[A-Z]{2}$`,
		"Address.countryCode":          `^[A-Z]{2}$`,
		"Address.latitude":             `^-?\d+\.\d{6}$`,
		"Address.fullAddress":          `^\d+ .+, .+, [A-Z]{2} \d{5}$`,
		"Internet.emailAddress":        `^[a-z]+\.[a-z]+@[a-z]+\.[a-z]+$`,
		"Internet.safeEmailAddress":    `^[a-z]+\.[a-z]+@example\.[a-z]+$`,
		"Internet.domainName":          `^[a-z]+\.[a-z]+$`,
		"Internet.url":                 `^https://www\.[a-z]+\.[a-z]+/[a-z]+$`,
		"Internet.password":            `^[A-Za-z0-9]{12}$`,
		"Internet.ipV4Address":         `^\d+\.\d+\.\d+\.\d+$`,
		"Internet.ipV6Address":         `^([0-9a-f]+:){7}[0-9a-f]+$`,
		"Internet.macAddress":          `^([0-9a-f]{2}:){5}[0-9a-f]{2}$`,
		"Internet.uuid":                `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
		"Company.name":                 `^\S+ .+$`,
		"Company.catchPhrase":          `^[A-Z]\w+ \w+ \w+$`,
		"Lorem.word":                   `^[a-z]+$`,
		"Lorem.words":                  `^\w+ \w+ \w+$`,
		"Lorem.words 5":                `^(\w+ ){4}\w+$`,
		"Lorem.sentence":               `^[A-Z][a-z ]+\.$`,
		"Lorem.paragraph":              `^[A-Z].+\.$`,
		"Lorem.characters 10":          `^[A-Za-z0-9]{10}$`,
		"Number.randomDigit":           `^\d$`,
		"Number.randomDigitNotZero":    `^[1-9]$`,
		"Number.digits 6":              `^\d{6}$`,
		"Number.numberBetween '5','7'": `^[56]$`,
		"PhoneNumber.cellPhone":        `^\d{3}-\d{3}-\d{4}$`,
		"Bool.bool":                    `^(true|false)$`,
		"Color.hex":                    `^#[0-9a-f]{6}$`,
		"#{Name.firstName}":            `^[A-Z][a-z]+$`,
	}
	for expr, shape := range shapes {
		re := regexp.MustCompile(shape)
		for range 25 { // the values are random, so look at a few
			got := render(t, e, `{{random "`+expr+`"}}`)
			if !re.MatchString(got) {
				t.Errorf("random %q = %q, want to match %s", expr, got, shape)
				break
			}
		}
	}
}

func TestFakerValuesVary(t *testing.T) {
	t.Parallel()
	e := template.New(clock.Real{})
	e.EnableFaker()
	seen := map[string]bool{}
	for range 40 {
		seen[render(t, e, `{{random 'Name.firstName'}}`)] = true
	}
	if len(seen) < 5 {
		t.Errorf("40 names gave only %d different ones", len(seen))
	}
}

func TestFakerMistakesAreWrittenIntoTheResponse(t *testing.T) {
	t.Parallel()
	e := template.New(clock.Real{})
	e.EnableFaker()
	for src, want := range map[string]string{
		`{{random 'Nope.thing'}}`:               "[ERROR: Unable to evaluate the expression Nope.thing]",
		`{{random 'Name'}}`:                     "[ERROR: Unable to evaluate the expression Name]",
		`{{random 'Number.numberBetween 5'}}`:   "[ERROR: Unable to evaluate the expression Number.numberBetween 5]",
		`{{random 'Lorem.words 99999'}}`:        "[ERROR: Unable to evaluate the expression Lorem.words 99999]",
		`{{random 'Number.numberBetween 7,5'}}`: "[ERROR: Unable to evaluate the expression Number.numberBetween 7,5]",
	} {
		if got := render(t, e, src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

func TestFakerHelperIsOffUntilEnabled(t *testing.T) {
	t.Parallel()
	if _, err := template.New(clock.Real{}).Render(`{{random 'Name.firstName'}}`, map[string]any{}); err == nil {
		t.Error("the random helper must not exist until the extension is on")
	}
}

func testRand() io.Reader { return rand.Reader }
