package acceptance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/auth/authtest"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// The managed-mode specs run the engine as the hosted service does, on one instance with the stores in memory
// (managed.singleInstance). The management app is played by the scenario: it holds the issuer's signing key,
// writes orgs, keys and the global layer into the engine's stores, and signs the tokens it presents.
const (
	managedIssuerURL = "https://shadowmock.example"
	managedAudience  = "mocks-test"
)

// managedWorld is the managed-mode state of a scenario.
type managedWorld struct {
	name     string
	audience string
	replay   bool
	pinned   bool

	// The issuer signs with signer. Every key in published is served at the key set URL.
	signer    *authtest.Issuer
	published []*authtest.Issuer
	jwksMu    sync.Mutex
	jwks      *httptest.Server

	orgs map[string]*orgRecord
	keys map[string]string // label -> the key to present

	// cred is the credential the scenario presents unless a step names another. lastBearer is what it sent last.
	cred       *credential
	defaultOrg string
	lastBearer string
	cfgIndex   int
	// writeErr is how the management app's last write to the org directory went.
	writeErr error
	// lastMetadata is the metadata the management app last wrote, for a step that reads it back.
	lastMetadata map[string]any
}

type orgRecord struct {
	id, handle, plan string
}

// credential says how a call authenticates; bearer makes the value to send, so a token is minted when it is used.
type credential struct {
	org    string // the org the credential is for; the call goes to this org unless a step says otherwise
	bearer func() (string, error)
}

func (w *world) mgd() (*managedWorld, error) {
	if w.m == nil {
		return nil, fmt.Errorf("this step needs a managed-mode engine: start with \"Shadow Mock is running in managed mode with the trusted issuer\"")
	}
	return w.m, nil
}

// startManaged sets the scenario up as managed mode with one trusted issuer.
func (w *world) startManaged(name string) error {
	signer := authtest.NewIssuer(name, managedIssuerURL, managedAudience, w.clk)
	m := &managedWorld{
		name: name, audience: managedAudience, signer: signer, published: []*authtest.Issuer{signer},
		orgs: map[string]*orgRecord{}, keys: map[string]string{},
	}
	m.jwks = httptest.NewServer(http.HandlerFunc(m.serveJWKS))
	w.m = m
	m.cfgIndex = len(w.yamls)
	w.yamls = append(w.yamls, m.yaml())
	return nil
}

// yaml is the engine configuration for the scenario's managed-mode settings.
func (m *managedWorld) yaml() string {
	keys := "jwksUrl: " + m.jwks.URL
	if m.pinned {
		keys = "publicKey: |\n" + indent(m.signer.PublicPEM(), "        ")
	}
	return fmt.Sprintf(`mode: managed
managed: { singleInstance: true }
admin: { globalWrites: deny }
mappings: { unknownFields: keep }
egress: { blockPrivate: true }
auth:
  issuers:
    - name: %s
      issuer: %s
      audience: %s
      replayCheck: %v
      %s
`, m.name, managedIssuerURL, m.audience, m.replay, strings.ReplaceAll(strings.TrimRight(keys, "\n"), "\n", "\n      "))
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// reconfigureManaged applies changed managed settings by restarting the engine; the setup steps are replayed.
func (w *world) reconfigureManaged() error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	w.yamls[m.cfgIndex] = m.yaml()
	if w.app == nil {
		return nil
	}
	if err := w.stop(); err != nil {
		return err
	}
	return w.startWithReplay()
}

// serveJWKS publishes the issuer's keys.
func (m *managedWorld) serveJWKS(rw http.ResponseWriter, _ *http.Request) {
	m.jwksMu.Lock()
	defer m.jwksMu.Unlock()
	set := struct {
		Keys []map[string]string `json:"keys"`
	}{}
	for _, i := range m.published {
		raw, err := i.Key.PublicKey.Bytes() // 0x04, X, Y
		if err != nil || len(raw) != 65 {
			continue
		}
		set.Keys = append(set.Keys, map[string]string{
			"kty": "EC", "crv": "P-256", "kid": i.Kid, "alg": "ES256", "use": "sig",
			"x": base64.RawURLEncoding.EncodeToString(raw[1:33]), "y": base64.RawURLEncoding.EncodeToString(raw[33:]),
		})
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(set) //nolint:errcheck // the client may have gone
}

// planLimit is what a plan allows that the engine enforces itself (FR-11.5, FR-11.6).
func planLimit(plan string) tenant.Limits {
	switch strings.ToLower(plan) {
	case "free":
		return tenant.Limits{RequestsPerSecond: 10, Burst: 10, MonthlyRequestCap: 100_000}
	case "starter":
		return tenant.Limits{RequestsPerSecond: 100, Burst: 100}
	case "team":
		return tenant.Limits{RequestsPerSecond: 200, Burst: 200}
	default: // Business and anything else
		return tenant.Limits{RequestsPerSecond: 2000, Burst: 2000}
	}
}

// addOrg creates an org in the engine's directory, as the management app would.
func (w *world) addOrg(name, handle, plan string, rps float64, burst int) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	rec := &orgRecord{id: name, handle: handle, plan: plan}
	m.orgs[name] = rec
	if m.defaultOrg == "" {
		m.defaultOrg = name
	}
	return w.record(func() error {
		lim := planLimit(plan)
		if rps > 0 {
			lim = tenant.Limits{RequestsPerSecond: rps, Burst: burst}
		}
		return w.app.Directory().Put(tenant.Org{ID: tenant.ID(name), Handle: handle, Limits: lim})
	})
}

func (m *managedWorld) org(name string) (*orgRecord, error) {
	if o, ok := m.orgs[name]; ok {
		return o, nil
	}
	return nil, fmt.Errorf("no org %q has been created in this scenario", name)
}

// addKey creates a Tool app key for an org and remembers the key to present under its label.
func (w *world) addKey(label, org string, scopes []string) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	if _, err = m.org(org); err != nil {
		return err
	}
	var secret string
	return w.record(func() error {
		if secret == "" {
			secret = authtest.NewKey(w.app.Keys(), tenant.ID(org), label, scopes...)
			m.keys[label] = secret
			return nil
		}
		// After a restart the same key is added again to the new store.
		w.app.Keys().Add(auth.APIKey{Hash: auth.HashKey(secret), Org: tenant.ID(org), Label: label, Scopes: scopes})
		return nil
	})
}

// tokenSpec describes a token to mint: a person in an org with an app role, claims the scenario replaces (a nil
// value leaves the claim out), and optionally another way of signing.
type tokenSpec struct {
	sub, org, role, scope string
	set                   authtest.Claims
	sign                  func(signer *authtest.Issuer, claims authtest.Claims) string
}

// mint makes the token. The claims are the issuer's defaults, the person, and then the scenario's changes.
func (m *managedWorld) mint(w *world, sp tokenSpec) string {
	now := w.clk.Now()
	claims := authtest.Claims{
		"iss": managedIssuerURL, "aud": m.audience, "iat": now.Unix(), "exp": now.Add(60 * time.Second).Unix(),
		"jti": fmt.Sprintf("jti-%d", now.UnixNano()), "sub": sp.sub, "org_id": sp.org, "app_role": sp.role,
	}
	if sp.scope != "" {
		claims["scope"] = sp.scope
	}
	for k, v := range sp.set {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	if sp.sign != nil {
		return sp.sign(m.signer, claims)
	}
	return m.signer.SignClaims(claims)
}

// hs256 signs a token with a shared secret, the way an attacker who has only the public key would.
func hs256(secret []byte, claims authtest.Claims) string {
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"}) //nolint:errcheck // plain values
	b, _ := json.Marshal(claims)                                       //nolint:errcheck // plain values
	signed := enc.EncodeToString(h) + "." + enc.EncodeToString(b)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signed))
	return signed + "." + enc.EncodeToString(mac.Sum(nil))
}

// presentToken makes a token the scenario's credential.
func (w *world) presentToken(sp tokenSpec) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	m.cred = &credential{bearer: func() (string, error) { return m.mint(w, sp), nil }}
	if _, known := m.orgs[sp.org]; known {
		m.defaultOrg = sp.org // a token for an org that does not exist is sent to the org the scenario is about
	}
	return nil
}

// adminURL is the admin URL of an org (by its name here, resolved to its handle).
func (m *managedWorld) adminURL(w *world, org, path string) (string, error) {
	o, err := m.org(org)
	if err != nil {
		return "", err
	}
	return w.srv.URL + "/" + o.handle + w.adminPrefixOnly() + w.sub(path), nil
}

// adminPrefixOnly is the configured admin prefix, without the base.
func (w *world) adminPrefixOnly() string { return w.adminPrefix() }

// managedAdminCall sends an admin request to an org with a credential.
func (w *world) managedAdminCall(method, path, body, org string, cred *credential, header http.Header) (*reply, error) {
	m, err := w.mgd()
	if err != nil {
		return nil, err
	}
	if ensureErr := w.ensure(); ensureErr != nil {
		return nil, ensureErr
	}
	seg := "" // the handle in the URL, when the step gave a whole URL
	if mm := fullAdminURL.FindStringSubmatch(path); mm != nil {
		seg, path = mm[1], mm[2]
		org = m.orgOfHandle(w, mm[1])
	}
	if org == "" {
		switch {
		case cred != nil && cred.org != "":
			org = cred.org
		default:
			org = m.defaultOrg
		}
	}
	target := w.srv.URL + "/" + seg + w.adminPrefix() + w.sub(path)
	if seg == "" {
		if target, err = m.adminURL(w, org, path); err != nil {
			return nil, err
		}
	}
	if body == "a minimal mapping" {
		body = `{"request":{"url":"/a"},"response":{"status":200}}`
	}
	r := newRequest(method, target)
	if body != "" {
		r.body = []byte(w.sub(body))
		r.header.Set("Content-Type", "application/json")
	}
	if cred != nil {
		v, bErr := cred.bearer()
		if bErr != nil {
			return nil, bErr
		}
		m.lastBearer = v
		r.header.Set("Authorization", "Bearer "+v)
	}
	for k, vs := range header {
		for _, v := range vs {
			r.header.Set(k, v)
		}
	}
	return w.send(r)
}

// callWith sends an admin call with the scenario's current credential unless one is given.
func (w *world) callWith(method, path, body string, cred *credential) error {
	m, err := w.mgd()
	if err != nil {
		return err
	}
	if cred == nil {
		cred = m.cred
	}
	_, err = w.managedAdminCall(method, path, body, "", cred, nil)
	return err
}

// keyCred is a credential that presents the key with the label.
func (m *managedWorld) keyCred(label string) *credential {
	return &credential{bearer: func() (string, error) {
		k, ok := m.keys[label]
		if !ok {
			return "", fmt.Errorf("no Tool app key %q has been created", label)
		}
		return k, nil
	}}
}

// ctxBackground is the context steps use for calls into the engine.
func ctxBackground() context.Context { return context.Background() }

func (w *world) initManaged(sc *godog.ScenarioContext) {
	w.initManagedAuth(sc)
	w.initManagedOrg(sc)
	w.initManagedKeys(sc)
	w.initManagedTenancy(sc)
	w.initManagedLayers(sc)
	w.initManagedLimits(sc)
	w.initManagedLimits2(sc)
}

// eventually runs check until it passes or the time is up, waiting in real time between tries. It returns the last
// failure.
func (w *world) eventually(within time.Duration, check func() error) error {
	deadline := time.Now().Add(within)
	for {
		err := check()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
