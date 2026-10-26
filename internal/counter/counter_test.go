package counter

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

var (
	global = store.Global(tenant.Local)
	sessA  = store.Scope{Tenant: tenant.Local, SessionID: "ses_A"}
	sessB  = store.Scope{Tenant: tenant.Local, SessionID: "ses_B"}
	now    = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
)

func next(t *testing.T, c Counters, k Key) int64 {
	t.Helper()
	n, err := c.Next(context.Background(), k, now)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCountersAreScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := NewMemory()
	gStub := Key{RequestScope: global, StubScope: global, StubID: "s1"}
	aStub := Key{RequestScope: sessA, StubScope: global, StubID: "s1"}
	for want := int64(1); want <= 3; want++ {
		if got := next(t, c, gStub); got != want {
			t.Errorf("global call = %d, want %d", got, want)
		}
	}
	if got := next(t, c, aStub); got != 1 {
		t.Errorf("session's first call = %d, want 1 (FR-7.6)", got)
	}
	if n := peek(t, c, aStub); n != 2 {
		t.Errorf("Peek = %d, want 2", n)
	}
	if n := peek(t, c, Key{RequestScope: sessB, StubID: "none"}); n != 1 {
		t.Errorf("Peek(new) = %d, want 1", n)
	}
	list, err := c.List(ctx, sessA)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]Entry{{Key: aStub, Count: 1, LastCalledAt: now}}, list); diff != "" {
		t.Errorf("List (-want +got):\n%s", diff)
	}
	if err := c.Set(ctx, gStub, 9); err != nil {
		t.Fatal(err)
	}
	if got := next(t, c, gStub); got != 10 {
		t.Errorf("after Set(9) call = %d, want 10", got)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := NewMemory()
	k1 := Key{RequestScope: sessA, StubScope: sessA, StubID: "s1"}
	k2 := Key{RequestScope: sessA, StubScope: global, StubID: "s2"}
	k3 := Key{RequestScope: global, StubScope: global, StubID: "s2"}
	for _, k := range []Key{k1, k2, k3} {
		next(t, c, k)
	}
	if err := c.Reset(ctx, sessA, "s1"); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k1) != 1 || next(t, c, k2) != 2 {
		t.Error("Reset(stub) should zero only that stub")
	}
	if err := c.Reset(ctx, sessA, ""); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k2) != 1 || next(t, c, k3) != 2 {
		t.Error("Reset(scope) should zero only that scope")
	}
	if err := c.ForgetStub(ctx, global, "s2"); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k2) != 1 || next(t, c, k3) != 1 {
		t.Error("ForgetStub should drop the stub's counters in every scope")
	}
	if err := c.ForgetScope(ctx, sessA); err != nil {
		t.Fatal(err)
	}
	if list := listOf(t, c, sessA); len(list) != 0 {
		t.Errorf("ForgetScope left %v", list)
	}
}

func TestListOrder(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	for _, k := range []Key{
		{RequestScope: sessA, StubScope: global, StubID: "b"},
		{RequestScope: sessA, StubScope: global, StubID: "a"},
		{RequestScope: sessA, StubScope: sessA, StubID: "a"},
	} {
		next(t, c, k)
	}
	list := listOf(t, c, sessA)
	got := make([]string, 0, len(list))
	for _, e := range list {
		got = append(got, e.Key.StubID+"@"+e.Key.StubScope.SessionID)
	}
	if diff := cmp.Diff([]string{"a@ses_A", "a@", "b@"}, got); diff != "" {
		t.Errorf("List order (-want +got):\n%s", diff)
	}
}

// TestConcurrentCallsAreGapFree checks that parallel calls get distinct, gap-free numbers.
func TestConcurrentCallsAreGapFree(t *testing.T) {
	t.Parallel()
	c := NewMemory()
	k := Key{RequestScope: sessA, StubScope: global, StubID: "s"}
	const calls = 500
	got := make([]int64, calls)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			n, err := c.Next(context.Background(), k, now)
			if err != nil {
				t.Error(err)
			}
			got[i] = n
		})
	}
	wg.Wait()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	for i, n := range got {
		if n != int64(i+1) {
			t.Fatalf("call numbers have a gap or duplicate at %d: %d", i, n)
		}
	}
}

func peek(t *testing.T, c Counters, k Key) int64 {
	t.Helper()
	n, err := c.Peek(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func listOf(t *testing.T, c Counters, s store.Scope) []Entry {
	t.Helper()
	l, err := c.List(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
