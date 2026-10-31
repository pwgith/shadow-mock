package tenant_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/tenant"
)

func TestHandleRules(t *testing.T) {
	t.Parallel()
	valid := []string{"acme", "acme-qa", "a1b2c3", "abc", "a-b"}
	for _, h := range valid {
		if err := tenant.ValidateHandle(h); err != nil {
			t.Errorf("%q should be valid: %v", h, err)
		}
	}
	invalid := []string{"ab", "1acme", "-acme", "acme-", "Acme", "ac_me", "_acme", "__admin", "ac me", "acme/other", "a-very-long-handle-that-goes-on-and-on-and-on"}
	for _, h := range invalid {
		if err := tenant.ValidateHandle(h); !errors.Is(err, tenant.ErrHandleInvalid) {
			t.Errorf("%q should be invalid, got %v", h, err)
		}
	}
	for _, h := range []string{"www", "api", "admin", "app", "mail", "static", "d"} {
		if err := tenant.ValidateHandle(h); !errors.Is(err, tenant.ErrHandleReserved) {
			t.Errorf("%q should be reserved, got %v", h, err)
		}
	}
}

func TestDirectoryRules(t *testing.T) {
	t.Parallel()
	d := tenant.NewMemory()
	if err := d.Put(tenant.Org{ID: "o1", Handle: "acme"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(tenant.Org{ID: "o2", Handle: "acme"}); !errors.Is(err, tenant.ErrHandleTaken) {
		t.Errorf("another org may not take a handle, got %v", err)
	}
	// A renamed handle stays as an alias that nobody else may take.
	if err := d.Put(tenant.Org{ID: "o1", Handle: "acme-qa"}); err != nil {
		t.Fatal(err)
	}
	if o, ok := d.Lookup("acme"); !ok || o.ID != "o1" {
		t.Errorf("the old handle should still resolve to o1, got %v %v", o, ok)
	}
	if err := d.Put(tenant.Org{ID: "o3", Handle: "acme"}); !errors.Is(err, tenant.ErrHandleTaken) {
		t.Errorf("an alias may not be taken, got %v", err)
	}
	// A deleted org is a tombstone: it is not changed or recreated, and its handles stay reserved.
	if err := d.MarkDeleted("o1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(tenant.Org{ID: "o1", Handle: "acme-qa", Status: tenant.StatusActive}); !errors.Is(err, tenant.ErrDeleted) {
		t.Errorf("a deleted org must not come back, got %v", err)
	}
	if err := d.SetStatus("o1", tenant.StatusActive); !errors.Is(err, tenant.ErrDeleted) {
		t.Errorf("a deleted org's status must not change, got %v", err)
	}
	if err := d.Put(tenant.Org{ID: "o4", Handle: "acme"}); !errors.Is(err, tenant.ErrHandleReserved) {
		t.Errorf("a deleted org's handle stays reserved, got %v", err)
	}
	if err := d.MarkDeleted("o1"); err != nil {
		t.Errorf("deleting twice is fine: %v", err)
	}
}

func TestSnapshotsDoNotChange(t *testing.T) {
	t.Parallel()
	d := tenant.NewMemory()
	_ = d.Put(tenant.Org{ID: "o1", Handle: "acme"})
	before, _ := d.Lookup("acme")
	_ = d.SetStatus("o1", tenant.StatusSuspended)
	after, _ := d.Lookup("acme")
	if before.Status == tenant.StatusSuspended || after.Status != tenant.StatusSuspended {
		t.Errorf("a snapshot must not change under a reader: before %q, after %q", before.Status, after.Status)
	}
}

func TestStatusServes(t *testing.T) {
	t.Parallel()
	serves := map[tenant.Status]bool{
		"": true, tenant.StatusActive: true, tenant.StatusTrialing: true, tenant.StatusPastDue: true,
		tenant.StatusSuspended: false, tenant.StatusCancelled: false, tenant.StatusDeleted: false,
	}
	for s, want := range serves {
		if s.Serves() != want {
			t.Errorf("%q serves = %v, want %v", s, s.Serves(), want)
		}
	}
}

func TestHandleErrorsSayWhichRuleWasBroken(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"ab":      "3 to 40 characters",
		"Acme":    "lowercase only",
		"9lives":  "must start with a letter",
		"__admin": "reserved for the engine",
		"api":     "reserved",
		"d":       "reserved",
	}
	for handle, want := range tests {
		err := tenant.ValidateHandle(handle)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want it to say %q", handle, err, want)
		}
	}
}
