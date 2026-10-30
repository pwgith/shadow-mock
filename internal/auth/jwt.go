package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// jwt is a token split into its parts, not yet trusted.
type jwt struct {
	header jwtHeader
	claims Claims
	signed string // header.payload, which the signature covers
	sig    []byte
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Claims are the parts of a token the engine reads (FR-11.2).
type Claims struct {
	Subject  string       `json:"sub"`
	Issuer   string       `json:"iss"`
	Audience audience     `json:"aud"`
	OrgID    string       `json:"org_id"`   //nolint:tagliatelle // the claim names the management app signs
	AppRole  string       `json:"app_role"` //nolint:tagliatelle // the claim names the management app signs
	Scope    string       `json:"scope"`
	ID       string       `json:"jti"`
	Expires  *numericDate `json:"exp"`
	NotAfter *numericDate `json:"nbf"`
	IssuedAt *numericDate `json:"iat"`
}

// audience is a string or a list of strings.
type audience []string

// UnmarshalJSON reads one audience or a list.
func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("aud: %w", err)
	}
	*a = many
	return nil
}

// numericDate is seconds since the epoch, as JWT writes dates.
type numericDate struct{ time.Time }

// UnmarshalJSON reads seconds since the epoch.
func (d *numericDate) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("date: %w", err)
	}
	d.Time = time.Unix(int64(f), 0).UTC()
	return nil
}

var errMalformed = errors.New("the token is not a JWT")

// parseJWT splits and decodes a token. It checks nothing about who signed it.
func parseJWT(token string) (*jwt, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errMalformed
	}
	enc := base64.RawURLEncoding
	var t jwt
	hdr, err := enc.DecodeString(parts[0])
	if err != nil {
		return nil, errMalformed
	}
	body, err := enc.DecodeString(parts[1])
	if err != nil {
		return nil, errMalformed
	}
	if t.sig, err = enc.DecodeString(parts[2]); err != nil {
		return nil, errMalformed
	}
	if json.Unmarshal(hdr, &t.header) != nil || json.Unmarshal(body, &t.claims) != nil {
		return nil, errMalformed
	}
	t.signed = parts[0] + "." + parts[1]
	return &t, nil
}

// verify checks the signature against key. The algorithm is pinned to the key's type: a token cannot
// choose its own, so there is no "none" and no confusing a public key for a secret.
func (t *jwt) verify(key crypto.PublicKey) error {
	digest := sha256.Sum256([]byte(t.signed))
	switch k := key.(type) {
	case *rsa.PublicKey:
		if t.header.Alg != "RS256" {
			return errors.New("the algorithm does not match the key")
		}
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], t.sig)
	case *ecdsa.PublicKey:
		if t.header.Alg != "ES256" || k.Curve != elliptic.P256() || len(t.sig) != 64 {
			return errors.New("the algorithm does not match the key")
		}
		r, s := new(big.Int).SetBytes(t.sig[:32]), new(big.Int).SetBytes(t.sig[32:])
		if !ecdsa.Verify(k, digest[:], r, s) {
			return errors.New("the signature does not verify")
		}
		return nil
	case ed25519.PublicKey:
		if t.header.Alg != "EdDSA" {
			return errors.New("the algorithm does not match the key")
		}
		if !ed25519.Verify(k, []byte(t.signed), t.sig) {
			return errors.New("the signature does not verify")
		}
		return nil
	}
	return errors.New("the key is of a kind the engine does not verify with")
}

// ParsePublicKey reads a public key from PEM (PKIX, or an X.509 certificate).
func ParsePublicKey(text string) (crypto.PublicKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("not PEM")
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return k, nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("not a PKIX public key or a certificate")
	}
	return cert.PublicKey, nil
}
