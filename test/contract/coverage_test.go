package contract_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

// dummyFixtures fills case placeholders so paths can be matched offline.
var dummyFixtures = map[string]string{
	"{sessionId}":            "ses_01JCONTRACT0000000000000000",
	"{urlMockId}":            "abcdefghijklmnop",
	"{stubMappingId}":        "11111111-1111-4111-8111-111111111111",
	"{sessionStubMappingId}": "22222222-2222-4222-8222-222222222222",
	"{fileId}":               "contract-fixture.json",
}

func fill(s string) string {
	for k, v := range dummyFixtures {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// TestEveryOperationHasALiveCase fails when an operation is added to the contract without
// a live contract case, so new endpoints cannot ship unchecked.
func TestEveryOperationHasALiveCase(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	covered := map[string]bool{}
	for _, c := range contract.Cases {
		covered[c.OperationID] = true
	}
	for _, op := range s.Operations {
		if !covered[op.ID] {
			t.Errorf("operation %s (%s %s) has no case in test/contract/cases.go", op.ID, op.Method, op.Path)
		}
	}
}

// TestLiveCasesTargetTheirOperation checks that each case's method and path resolve to the
// operation it claims to exercise.
func TestLiveCasesTargetTheirOperation(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	for _, c := range contract.Cases {
		op := s.FindOperation(c.Method, fill(c.Path))
		switch {
		case op == nil:
			t.Errorf("%q: %s %s matches no operation", c.Name, c.Method, c.Path)
		case op.ID != c.OperationID:
			t.Errorf("%q: %s %s resolves to %s, not %s", c.Name, c.Method, c.Path, op.ID, c.OperationID)
		}
	}
}

// TestLiveCaseStatusesAreDocumented checks every expected status is in the contract.
func TestLiveCaseStatusesAreDocumented(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	for _, c := range contract.Cases {
		op := s.OperationByID(c.OperationID)
		if op == nil {
			t.Errorf("%q: unknown operation %s", c.Name, c.OperationID)
			continue
		}
		if len(c.WantStatus) == 0 {
			t.Errorf("%q: no expected status", c.Name)
		}
		for _, st := range c.WantStatus {
			if _, ok := op.Responses[strconv.Itoa(st)]; !ok {
				t.Errorf("%q: status %d is not documented for %s", c.Name, st, op.ID)
			}
		}
		if c.WantError != "" && !s.ErrorCodes()[c.WantError] {
			t.Errorf("%q: error code %s is not in the ErrorCode enum", c.Name, c.WantError)
		}
	}
}

// TestLiveCaseBodiesConformToTheContract checks that request bodies of cases expected to
// succeed are valid, so a failing live case points at the engine, not at the test.
func TestLiveCaseBodiesConformToTheContract(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	for _, c := range contract.Cases {
		if c.Body == "" || slices.ContainsFunc(c.WantStatus, func(st int) bool { return st >= http.StatusBadRequest }) {
			continue // negative cases send invalid bodies on purpose
		}
		ct := c.ContentType
		if ct == "" {
			ct = "application/json"
		}
		op := s.FindOperation(c.Method, fill(c.Path))
		if op == nil {
			continue // reported by TestLiveCasesTargetTheirOperation
		}
		ex := contract.Exchange{
			Method: c.Method, Path: fill(c.Path), RequestType: ct, RequestBody: []byte(c.Body),
			Status: c.WantStatus[0],
		}
		for _, v := range s.Check(ex) {
			if strings.Contains(v, "request body") {
				t.Errorf("%q: %s", c.Name, v)
			}
		}
	}
}

// TestCheckDetectsViolations proves the validator itself catches the classes of drift the
// live suite relies on.
func TestCheckDetectsViolations(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	tests := []struct {
		name    string
		ex      contract.Exchange
		wantErr string // substring; "" means no violation expected
	}{
		{
			name:    "valid session response",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/sessions/ses_01JCONTRACT0000000000000000", Status: 200, ResponseType: "application/json", ResponseBody: []byte(`{"id":"ses_01JCONTRACT0000000000000000","kind":"key-based","createdBy":"local","createdAt":"2026-11-01T09:00:00Z","expiresAt":"2026-11-01T10:00:00Z"}`)},
			wantErr: "",
		},
		{
			name:    "missing required response field",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/sessions/ses_01JCONTRACT0000000000000000", Status: 200, ResponseType: "application/json", ResponseBody: []byte(`{"id":"x","kind":"key-based"}`)},
			wantErr: "does not match the schema",
		},
		{
			name:    "wrong enum value",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/sessions/ses_01JCONTRACT0000000000000000", Status: 200, ResponseType: "application/json", ResponseBody: []byte(`{"id":"x","kind":"dedicated","createdBy":"local","createdAt":"2026-11-01T09:00:00Z","expiresAt":"2026-11-01T10:00:00Z"}`)},
			wantErr: "does not match the schema",
		},
		{
			name:    "undocumented status",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/mappings", Status: 418},
			wantErr: "status 418 is not documented",
		},
		{
			name:    "error body with an unknown code",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/sessions/ses_01JCONTRACT0000000000000000", Status: 404, ResponseType: "application/json", ResponseBody: []byte(`{"error":"no_such_code","message":"x"}`)},
			wantErr: "does not match the schema",
		},
		{
			name:    "undocumented content type",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/mappings", Status: 200, ResponseType: "text/html", ResponseBody: []byte("<html/>")},
			wantErr: "is not documented",
		},
		{
			name:    "invalid request body",
			ex:      contract.Exchange{Method: "POST", Path: "/__admin/sessions/key-based", RequestType: "application/json", RequestBody: []byte(`{"ttlSeconds":"soon"}`), Status: 201},
			wantErr: "request body",
		},
		{
			name:    "missing required request body",
			ex:      contract.Exchange{Method: "POST", Path: "/__admin/requests/count", Status: 200, ResponseType: "application/json", ResponseBody: []byte(`{"count":0}`)},
			wantErr: "request body is required",
		},
		{
			name:    "unknown path",
			ex:      contract.Exchange{Method: "GET", Path: "/__admin/nope", Status: 200},
			wantErr: "no operation in the contract",
		},
		{
			name:    "literal path wins over a template",
			ex:      contract.Exchange{Method: "POST", Path: "/__admin/mappings/import", RequestType: "application/json", RequestBody: []byte(`{"mappings":[]}`), Status: 200},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(s.Check(tt.ex), "\n")
			switch {
			case tt.wantErr == "" && got != "":
				t.Errorf("Check() = %q, want no violations", got)
			case tt.wantErr != "" && !strings.Contains(got, tt.wantErr):
				t.Errorf("Check() = %q, want a violation containing %q", got, tt.wantErr)
			}
		})
	}
}
