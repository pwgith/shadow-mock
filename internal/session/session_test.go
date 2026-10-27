package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/apierr"
	"github.com/pwgith/shadow-mock/internal/clock"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var start = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func newRegistry() (*Registry, *clock.Fake) {
	c := clock.NewFake(start)
	return NewRegistry(c, DefaultLimits()), c
}

func ttl(n int64) *int64 { return &n }

func code(err error) apierr.Code {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := newRegistry()
	s, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: KeyBased, Name: "n", Tokens: []string{"CUST-1"}, CreatedBy: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Key) != 30 || s.KeyPrefix() != s.Key[:8] || !s.ExpiresAt.Equal(start.Add(time.Hour)) || s.TTL != time.Hour {
		t.Errorf("Create = %+v", s)
	}
	u, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: URLBased})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.ID) != 16 || u.Key != "" || !u.ExpiresAt.Equal(start.Add(24*time.Hour)) {
		t.Errorf("Create url-based = %+v", u)
	}
	tests := []struct {
		name string
		req  CreateRequest
		want apierr.Code
	}{
		{"zero TTL", CreateRequest{Kind: KeyBased, TTLSeconds: ttl(0)}, apierr.TTLMustBePositive},
		{"TTL above maximum", CreateRequest{Kind: KeyBased, TTLSeconds: ttl(86401)}, apierr.TTLExceedsMaximum},
		{"URL-based TTL above maximum", CreateRequest{Kind: URLBased, TTLSeconds: ttl(2592001)}, apierr.TTLExceedsMaximum},
		{"short token", CreateRequest{Kind: KeyBased, Tokens: []string{"AB"}}, apierr.TokenTooShort},
		{"token in use", CreateRequest{Kind: KeyBased, Tokens: []string{"CUST-1"}}, apierr.TokenInUse},
		{"token equal to a key", CreateRequest{Kind: KeyBased, Tokens: []string{s.Key}}, apierr.TokenInUse},
		{"unknown parent", CreateRequest{Kind: KeyBased, ParentID: "nope"}, apierr.ParentNotFound},
		{"parent is key-based", CreateRequest{Kind: KeyBased, ParentID: s.ID}, apierr.ParentNotFound},
	}
	for _, tt := range tests {
		if _, err := r.Create(ctx, tenant.Local, tt.req); code(err) != tt.want {
			t.Errorf("%s: error %v, want %s", tt.name, err, tt.want)
		}
	}
	// The same token is fine in another org.
	if _, err := r.Create(ctx, "other", CreateRequest{Kind: KeyBased, Tokens: []string{"CUST-1"}}); err != nil {
		t.Errorf("token in another org: %v", err)
	}
}

func TestNestedSessionExpiryIsCapped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _ := newRegistry()
	parent, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: URLBased, TTLSeconds: ttl(600)})
	if err != nil {
		t.Fatal(err)
	}
	child, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: KeyBased, ParentID: parent.ID, TTLSeconds: ttl(3600)})
	if err != nil {
		t.Fatal(err)
	}
	if !child.ExpiresAt.Equal(parent.ExpiresAt) {
		t.Errorf("child expires %v, want the parent's %v", child.ExpiresAt, parent.ExpiresAt)
	}
	if s, kaErr := r.KeepAlive(ctx, tenant.Local, child.ID); kaErr != nil || !s.ExpiresAt.Equal(parent.ExpiresAt) {
		t.Errorf("keepalive moved the child past its parent: %v", s.ExpiresAt)
	}
	scopes, err := r.Remove(ctx, tenant.Local, parent.ID)
	if err != nil || len(scopes) != 2 {
		t.Errorf("Remove(parent) = %v, %v, want the parent and its child", scopes, err)
	}
	if _, ok := r.Get(tenant.Local, child.ID); ok {
		t.Error("child survived its parent")
	}
}

func TestLookupAndExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := newRegistry()
	s, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: KeyBased, TTLSeconds: ttl(60)})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Resolve(tenant.Local, s.Key); !ok || got.ID != s.ID {
		t.Error("Resolve by key failed")
	}
	if got, ok := r.Resolve(tenant.Local, s.ID); !ok || got.ID != s.ID {
		t.Error("Resolve by id failed")
	}
	if _, ok := r.Resolve(tenant.Local, "smk_nope"); ok {
		t.Error("Resolve of an unknown key succeeded")
	}
	c.Advance(2 * time.Second)
	u := mustCreate(t, r, CreateRequest{Kind: URLBased})
	if l := r.List(tenant.Local, ""); len(l) != 2 || l[0].ID != u.ID {
		t.Errorf("List = %v, want newest first", l)
	}
	if l := r.List(tenant.Local, URLBased); len(l) != 1 || len(r.All()) != 2 {
		t.Errorf("List(url-based) = %v", l)
	}
	c.Advance(time.Minute)
	if _, ok := r.Get(tenant.Local, s.ID); ok {
		t.Error("expired session still visible")
	}
	if _, err := r.Update(ctx, tenant.Local, s.ID, Patch{}); code(err) != apierr.NotFound {
		t.Errorf("Update(expired) = %v", err)
	}
	if _, err := r.KeepAlive(ctx, tenant.Local, s.ID); code(err) != apierr.NotFound {
		t.Errorf("KeepAlive(expired) = %v", err)
	}
	if _, err := r.Remove(ctx, tenant.Local, s.ID); code(err) != apierr.NotFound {
		t.Errorf("Remove(expired) = %v", err)
	}
	if scopes := r.RemoveExpired(ctx); len(scopes) != 1 || scopes[0].SessionID != s.ID {
		t.Errorf("RemoveExpired = %v", scopes)
	}
	if r.Limits().MinTokenLength != 6 || s.String() == "" {
		t.Error("Limits/String")
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, c := newRegistry()
	s := mustCreate(t, r, CreateRequest{Kind: KeyBased, Tokens: []string{"TOKEN-1"}})
	if _, err := r.Create(ctx, tenant.Local, CreateRequest{Kind: KeyBased, Tokens: []string{"TOKEN-X"}}); err != nil {
		t.Fatal(err)
	}
	u := mustCreate(t, r, CreateRequest{Kind: URLBased})
	c.Advance(time.Minute)
	name := "renamed"
	got, err := r.Update(ctx, tenant.Local, s.ID, Patch{
		Name: &name, TTLSeconds: ttl(120), AddTokens: []string{"TOKEN-2", "TOKEN-1"}, RemoveTokens: []string{"TOKEN-1"},
		AddMasks: []model.Mask{{ID: "m1"}, {ID: "m2"}}, RemoveMasks: []model.Mask{{ID: "m2"}}, Metadata: map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "renamed" || len(got.Tokens) != 1 || got.Tokens[0] != "TOKEN-2" || len(got.Masks) != 1 ||
		!got.ExpiresAt.Equal(start.Add(time.Minute+2*time.Minute)) || got.Metadata["a"] != 1 {
		t.Errorf("Update = %+v", got)
	}
	errs := map[string]struct {
		id   string
		p    Patch
		want apierr.Code
	}{
		"token of another session": {s.ID, Patch{AddTokens: []string{"TOKEN-X"}}, apierr.TokenInUse},
		"bad TTL":                  {s.ID, Patch{TTLSeconds: ttl(-1)}, apierr.TTLMustBePositive},
		"tokens on url-based":      {u.ID, Patch{AddTokens: []string{"TOKEN-9"}}, apierr.ValidationFailed},
	}
	for name, tt := range errs {
		if _, updErr := r.Update(ctx, tenant.Local, tt.id, tt.p); code(updErr) != tt.want {
			t.Errorf("%s: %v, want %s", name, updErr, tt.want)
		}
	}
	c.Advance(time.Minute)
	k, err := r.KeepAlive(ctx, tenant.Local, s.ID)
	if err != nil || !k.ExpiresAt.Equal(c.Now().Add(2*time.Minute)) {
		t.Errorf("KeepAlive = %v, %v", k.ExpiresAt, err)
	}
}

func mustCreate(t *testing.T, r *Registry, req CreateRequest) Session {
	t.Helper()
	s, err := r.Create(context.Background(), tenant.Local, req)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
