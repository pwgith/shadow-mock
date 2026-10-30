package acceptance

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/cucumber/godog"
)

// initExtensions registers the steps that check what the jwt helpers made (FR-10.16).
func (w *world) initExtensions(sc *godog.ScenarioContext) {
	sc.Step(`^the response body is a JWT signed with HS256 by the secret "([^"]*)" and claims:$`, func(secret string, table *godog.Table) error {
		tok, err := w.lastToken()
		if err != nil {
			return err
		}
		if tok.header["alg"] != "HS256" {
			return fmt.Errorf("alg = %v, want HS256", tok.header["alg"])
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(tok.signed))
		if !hmac.Equal(tok.sig, mac.Sum(nil)) {
			return errors.New("the signature does not verify with the secret")
		}
		return tok.hasClaims(table)
	})
	sc.Step(`^the response body is a JWT with claims:$`, func(table *godog.Table) error {
		tok, err := w.lastToken()
		if err != nil {
			return err
		}
		return tok.hasClaims(table)
	})
	sc.Step(`^the response body is a JWT signed with RS256 by the key at "([^"]*)"$`, func(keysPath string) error {
		tok, err := w.lastToken()
		if err != nil {
			return err
		}
		if tok.header["alg"] != "RS256" {
			return fmt.Errorf("alg = %v, want RS256", tok.header["alg"])
		}
		saved, history := w.last, w.history
		defer func() { w.last, w.history = saved, history }() // fetching the keys is not a step of the scenario
		keys, err := w.send(newRequest("GET", keysPath))
		if err != nil {
			return err
		}
		return tok.verifiesAgainst(keys.body)
	})
}

// token is a JWT read from a response.
type token struct {
	header, claims map[string]any
	signed         string
	sig            []byte
}

func (w *world) lastToken() (*token, error) {
	if w.last == nil {
		return nil, errors.New("no response yet")
	}
	parts := strings.Split(strings.TrimSpace(string(w.last.body)), ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("the body is not a JWT: %q", w.last.body)
	}
	enc := base64.RawURLEncoding
	t := &token{signed: parts[0] + "." + parts[1]}
	for i, dst := range []*map[string]any{&t.header, &t.claims} {
		raw, err := enc.DecodeString(parts[i])
		if err != nil {
			return nil, fmt.Errorf("part %d is not base64url: %w", i+1, err)
		}
		if err = json.Unmarshal(raw, dst); err != nil {
			return nil, fmt.Errorf("part %d is not JSON: %w", i+1, err)
		}
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("the signature is not base64url: %w", err)
	}
	t.sig = sig
	return t, nil
}

// hasClaims checks a table of claim and value rows, comparing the JSON of each claim with the cell.
func (t *token) hasClaims(table *godog.Table) error {
	for _, row := range table.Rows {
		name, want := row.Cells[0].Value, row.Cells[1].Value
		got, ok := t.claims[name]
		if !ok {
			return fmt.Errorf("claim %q is missing from %v", name, t.claims)
		}
		if want == "<absent>" {
			return fmt.Errorf("claim %q should be absent, got %v", name, got)
		}
		b, err := json.Marshal(got)
		if err != nil {
			return err
		}
		if string(b) != want && fmt.Sprint(got) != want {
			return fmt.Errorf("claim %q = %s, want %s", name, b, want)
		}
	}
	return nil
}

// verifiesAgainst checks an RS256 token against the key set in body, the key named in the header.
func (t *token) verifiesAgainst(body []byte) error {
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("the key set is not JSON: %w", err)
	}
	enc := base64.RawURLEncoding
	for _, k := range set.Keys {
		if k.Kid != t.header["kid"] {
			continue
		}
		n, errN := enc.DecodeString(k.N)
		e, errE := enc.DecodeString(k.E)
		if errN != nil || errE != nil {
			return errors.New("the key is not base64url")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		digest := sha256.Sum256([]byte(t.signed))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], t.sig); err != nil {
			return fmt.Errorf("the signature does not verify with key %s: %w", k.Kid, err)
		}
		return nil
	}
	return fmt.Errorf("no key %v in the key set", t.header["kid"])
}
