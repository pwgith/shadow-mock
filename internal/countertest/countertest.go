// Package countertest is the suite every counter.Counters implementation must pass.
package countertest

import (
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func global() store.Scope { return store.Global(tenant.Local) }
func sessA() store.Scope  { return store.Scope{Tenant: tenant.Local, SessionID: "ses_A"} }
func sessB() store.Scope  { return store.Scope{Tenant: tenant.Local, SessionID: "ses_B"} }
func other() store.Scope  { return store.Global("globex") }
func now() time.Time      { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }

// sameStub is the id of a stub that two orgs both have.
const sameStub = "same"

// Run runs the suite against counters made by newCounters, which must give an empty store each time.
func Run(t *testing.T, newCounters func(t *testing.T) counter.Counters) {
	t.Helper()
	for name, fn := range map[string]func(*testing.T, counter.Counters){
		"counters are scoped":                  scoped,
		"set, peek and list":                   setPeekList,
		"reset, forget stub and forget scope":  resets,
		"list order":                           listOrder,
		"concurrent calls are gap free":        gapFree,
		"orgs do not share counters":           orgsApart,
		"forgetting one org's stub spares all": forgetSpares,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fn(t, newCounters(t))
		})
	}
}

func next(t *testing.T, c counter.Counters, k counter.Key) int64 {
	t.Helper()
	n, err := c.Next(t.Context(), k, now())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func list(t *testing.T, c counter.Counters, s store.Scope) []counter.Entry {
	t.Helper()
	l, err := c.List(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func scoped(t *testing.T, c counter.Counters) {
	t.Helper()
	gStub := counter.Key{RequestScope: global(), StubScope: global(), StubID: "s1"}
	aStub := counter.Key{RequestScope: sessA(), StubScope: global(), StubID: "s1"}
	for want := int64(1); want <= 3; want++ {
		if got := next(t, c, gStub); got != want {
			t.Errorf("global() call = %d, want %d", got, want)
		}
	}
	if got := next(t, c, aStub); got != 1 {
		t.Errorf("session's first call = %d, want 1 (FR-7.6)", got)
	}
}

func setPeekList(t *testing.T, c counter.Counters) {
	t.Helper()
	ctx := t.Context()
	gStub := counter.Key{RequestScope: global(), StubScope: global(), StubID: "s1"}
	aStub := counter.Key{RequestScope: sessA(), StubScope: global(), StubID: "s1"}
	next(t, c, aStub)
	if n, err := c.Peek(ctx, aStub); err != nil || n != 2 {
		t.Errorf("Peek = %d, %v, want 2", n, err)
	}
	if n, err := c.Peek(ctx, counter.Key{RequestScope: sessB(), StubID: "none"}); err != nil || n != 1 {
		t.Errorf("Peek(new) = %d, %v, want 1", n, err)
	}
	want := []counter.Entry{{Key: aStub, Count: 1, LastCalledAt: now()}}
	if diff := cmp.Diff(want, list(t, c, sessA())); diff != "" {
		t.Errorf("List (-want +got):\n%s", diff)
	}
	if err := c.Set(ctx, gStub, 9); err != nil {
		t.Fatal(err)
	}
	if got := next(t, c, gStub); got != 10 {
		t.Errorf("after Set(9) call = %d, want 10", got)
	}
}

func resets(t *testing.T, c counter.Counters) {
	t.Helper()
	ctx := t.Context()
	k1 := counter.Key{RequestScope: sessA(), StubScope: sessA(), StubID: "s1"}
	k2 := counter.Key{RequestScope: sessA(), StubScope: global(), StubID: "s2"}
	k3 := counter.Key{RequestScope: global(), StubScope: global(), StubID: "s2"}
	for _, k := range []counter.Key{k1, k2, k3} {
		next(t, c, k)
	}
	if err := c.Reset(ctx, sessA(), "s1"); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k1) != 1 || next(t, c, k2) != 2 {
		t.Error("Reset(stub) should zero only that stub")
	}
	if err := c.Reset(ctx, sessA(), ""); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k2) != 1 || next(t, c, k3) != 2 {
		t.Error("Reset(scope) should zero only that scope")
	}
	if err := c.ForgetStub(ctx, global(), "s2"); err != nil {
		t.Fatal(err)
	}
	if next(t, c, k2) != 1 || next(t, c, k3) != 1 {
		t.Error("ForgetStub should drop the stub's counters in every scope")
	}
	if err := c.ForgetScope(ctx, sessA()); err != nil {
		t.Fatal(err)
	}
	if l := list(t, c, sessA()); len(l) != 0 {
		t.Errorf("ForgetScope left %v", l)
	}
}

func listOrder(t *testing.T, c counter.Counters) {
	t.Helper()
	for _, k := range []counter.Key{
		{RequestScope: sessA(), StubScope: global(), StubID: "b"},
		{RequestScope: sessA(), StubScope: global(), StubID: "a"},
		{RequestScope: sessA(), StubScope: sessA(), StubID: "a"},
	} {
		next(t, c, k)
	}
	l := list(t, c, sessA())
	got := make([]string, 0, len(l))
	for _, e := range l {
		got = append(got, e.Key.StubID+"@"+e.Key.StubScope.SessionID)
	}
	if diff := cmp.Diff([]string{"a@ses_A", "a@", "b@"}, got); diff != "" {
		t.Errorf("List order (-want +got):\n%s", diff)
	}
}

func gapFree(t *testing.T, c counter.Counters) {
	t.Helper()
	k := counter.Key{RequestScope: sessA(), StubScope: global(), StubID: "s"}
	const calls = 200
	got := make([]int64, calls)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			n, err := c.Next(t.Context(), k, now())
			if err != nil {
				t.Error(err)
			}
			got[i] = n
		})
	}
	wg.Wait()
	seen := map[int64]bool{}
	for _, n := range got {
		if n < 1 || n > calls || seen[n] {
			t.Fatalf("call numbers must be 1..%d without repeats, got %d (seen: %v)", calls, n, seen[n])
		}
		seen[n] = true
	}
}

func orgsApart(t *testing.T, c counter.Counters) {
	t.Helper()
	a := counter.Key{RequestScope: global(), StubScope: global(), StubID: sameStub}
	b := counter.Key{RequestScope: other(), StubScope: other(), StubID: sameStub}
	next(t, c, a)
	next(t, c, a)
	if got := next(t, c, b); got != 1 {
		t.Errorf("another org's first call = %d, want 1", got)
	}
	if l := list(t, c, other()); len(l) != 1 || l[0].Count != 1 {
		t.Errorf("another org's list = %v", l)
	}
}

func forgetSpares(t *testing.T, c counter.Counters) {
	t.Helper()
	a := counter.Key{RequestScope: global(), StubScope: global(), StubID: sameStub}
	b := counter.Key{RequestScope: other(), StubScope: other(), StubID: sameStub}
	next(t, c, a)
	next(t, c, b)
	if err := c.ForgetStub(t.Context(), global(), "same"); err != nil {
		t.Fatal(err)
	}
	if got := next(t, c, b); got != 2 {
		t.Errorf("forgetting one org's stub reset another org's counter: call = %d, want 2", got)
	}
	if err := c.ForgetScope(t.Context(), global()); err != nil {
		t.Fatal(err)
	}
	if got := next(t, c, b); got != 3 {
		t.Errorf("forgetting one org's scope reset another org's counter: call = %d, want 3", got)
	}
}
