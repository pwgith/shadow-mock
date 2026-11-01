package audit_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/audit"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

func TestListIsNewestFirstAndOnlyTheOrgs(t *testing.T) {
	t.Parallel()
	trail := audit.New()
	ctx := t.Context()
	at := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	for i, org := range []tenant.ID{"acme", "globex", "acme"} {
		trail.Record(ctx, audit.Entry{At: at.Add(time.Duration(i) * time.Minute), Org: org, Subject: "sam", Method: "POST", Route: "/x", Status: 201})
	}
	got := trail.List(ctx, "acme")
	if len(got) != 2 || !got[0].At.After(got[1].At) {
		t.Fatalf("acme's entries = %+v, want 2, newest first", got)
	}
	if got := trail.List(ctx, "nobody"); got == nil || len(got) != 0 {
		t.Errorf("an org with no entries lists %#v, want an empty list that encodes as []", got)
	}
}

func TestAnonymiseChangesOnlyThatPersonInThatOrg(t *testing.T) {
	t.Parallel()
	trail := audit.New()
	ctx := t.Context()
	trail.Record(ctx, audit.Entry{Org: "acme", Subject: "sam"})
	trail.Record(ctx, audit.Entry{Org: "acme", Subject: "dana"})
	trail.Record(ctx, audit.Entry{Org: "acme", Subject: "sam"})
	trail.Record(ctx, audit.Entry{Org: "globex", Subject: "sam"})

	if n := trail.Anonymise(ctx, "acme", "sam"); n != 2 {
		t.Fatalf("anonymised %d entries, want 2", n)
	}
	subjects := map[tenant.ID][]string{}
	for _, org := range []tenant.ID{"acme", "globex"} {
		for _, e := range trail.List(ctx, org) {
			subjects[org] = append(subjects[org], e.Subject)
		}
	}
	if fmt.Sprint(subjects["acme"]) != fmt.Sprint([]string{audit.Anonymous, "dana", audit.Anonymous}) {
		t.Errorf("acme's subjects = %v", subjects["acme"])
	}
	if fmt.Sprint(subjects["globex"]) != "[sam]" {
		t.Errorf("globex's subjects = %v, want sam untouched", subjects["globex"])
	}
	if n := trail.Anonymise(ctx, "acme", "sam"); n != 0 {
		t.Errorf("a second anonymise changed %d entries, want 0", n)
	}
}

func TestTheTrailKeepsTheNewestEntriesWhenFull(t *testing.T) {
	t.Parallel()
	trail := audit.New()
	ctx := t.Context()
	const over = 100_000 + 5
	for i := range over {
		trail.Record(ctx, audit.Entry{Org: "acme", Status: i})
	}
	got := trail.List(ctx, "acme")
	if len(got) != 100_000 {
		t.Fatalf("kept %d entries, want 100000", len(got))
	}
	if got[0].Status != over-1 || got[len(got)-1].Status != 5 {
		t.Errorf("kept statuses %d down to %d, want %d down to 5", got[0].Status, got[len(got)-1].Status, over-1)
	}
}
