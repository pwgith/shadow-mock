package match

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/model"
)

func vp(t *testing.T, s string) model.ValuePattern {
	t.Helper()
	var p model.ValuePattern
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("pattern %s: %v", s, err)
	}
	return p
}

var fixedNow = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func opts() Options { return Options{Now: func() time.Time { return fixedNow }} }

func TestCompileString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		value   string
		present bool
		want    bool
	}{
		{"equalTo matches", `{"equalTo":"abc"}`, "abc", true, true},
		{"equalTo is case sensitive", `{"equalTo":"abc"}`, "ABC", true, false},
		{"equalTo caseInsensitive", `{"equalTo":"abc","caseInsensitive":true}`, "ABC", true, true},
		{"equalTo absent value", `{"equalTo":"abc"}`, "", false, false},
		{"contains", `{"contains":"b"}`, "abc", true, true},
		{"contains absent", `{"contains":"b"}`, "", false, false},
		{"doesNotContain", `{"doesNotContain":"z"}`, "abc", true, true},
		{"doesNotContain hit", `{"doesNotContain":"b"}`, "abc", true, false},
		{"doesNotContain absent", `{"doesNotContain":"b"}`, "", false, true},
		{"matches is anchored", `{"matches":"a.c"}`, "abc", true, true},
		{"matches whole string only", `{"matches":"b"}`, "abc", true, false},
		{"doesNotMatch", `{"doesNotMatch":"x.*"}`, "abc", true, true},
		{"doesNotMatch absent", `{"doesNotMatch":"x.*"}`, "", false, true},
		{"matches absent", `{"matches":".*"}`, "", false, false},
		{"absent true and missing", `{"absent":true}`, "", false, true},
		{"absent true and present", `{"absent":true}`, "x", true, false},
		{"absent false and present", `{"absent":false}`, "x", true, true},
		{"equalToNumber", `{"equalToNumber":5}`, "5.00", true, true},
		{"equalToNumber from string", `{"equalToNumber":"5"}`, "5", true, true},
		{"equalToNumber not a number", `{"equalToNumber":5}`, "five", true, false},
		{"greaterThanNumber", `{"greaterThanNumber":5}`, "6", true, true},
		{"greaterThanEqualNumber", `{"greaterThanEqualNumber":5}`, "5", true, true},
		{"lessThanNumber", `{"lessThanNumber":5}`, "5", true, false},
		{"lessThanEqualNumber", `{"lessThanEqualNumber":5}`, "4.9", true, true},
		{"number absent", `{"lessThanNumber":5}`, "", false, false},
		{"and", `{"and":[{"contains":"a"},{"contains":"c"}]}`, "abc", true, true},
		{"and fails", `{"and":[{"contains":"a"},{"contains":"z"}]}`, "abc", true, false},
		{"or", `{"or":[{"equalTo":"x"},{"equalTo":"abc"}]}`, "abc", true, true},
		{"or fails", `{"or":[{"equalTo":"x"},{"equalTo":"y"}]}`, "abc", true, false},
		{"not", `{"not":{"equalTo":"x"}}`, "abc", true, true},
		{"before absolute", `{"before":"2027-01-01T00:00:00Z"}`, "2026-12-31T23:59:59Z", true, true},
		{"after absolute", `{"after":"2027-01-01T00:00:00Z"}`, "2026-12-31T23:59:59Z", true, false},
		{"before relative", `{"before":"now +1 days"}`, "2026-10-27T13:00:00Z", true, true},
		{"after relative negative", `{"after":"now -1 hours"}`, "2026-10-27T11:30:00Z", true, true},
		{"equalToDateTime", `{"equalToDateTime":"2026-10-27T12:00:00Z"}`, "2026-10-27T12:00:00Z", true, true},
		{"actualFormat", `{"equalToDateTime":"2026-10-27T00:00:00Z","actualFormat":"dd/MM/yyyy"}`, "27/10/2026", true, true},
		{"truncateActual", `{"equalToDateTime":"2026-10-01T00:00:00Z","truncateActual":"first day of month"}`, "2026-10-27T09:15:00Z", true, true},
		{"truncateExpected", `{"before":"now","truncateExpected":"first hour of day"}`, "2026-10-26T23:00:00Z", true, true},
		{"expectedOffset", `{"after":"now","expectedOffset":-2,"expectedOffsetUnit":"days"}`, "2026-10-26T00:00:00Z", true, true},
		{"date HTTP format", `{"before":"2027-01-01T00:00:00Z"}`, "Tue, 27 Oct 2026 12:00:00 GMT", true, true},
		{"date unparseable", `{"before":"2027-01-01T00:00:00Z"}`, "soon", true, false},
		{"date absent", `{"before":"2027-01-01T00:00:00Z"}`, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := CompileString(vp(t, tt.pattern), opts())
			if err != nil {
				t.Fatalf("CompileString(%s) error: %v", tt.pattern, err)
			}
			if got := m.MatchString(tt.value, tt.present).Matched; got != tt.want {
				t.Errorf("MatchString(%q, %v) = %v, want %v", tt.value, tt.present, got, tt.want)
			}
		})
	}
}

func TestCompileStringErrors(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		`{}`,
		`{"unknownOp":"x"}`,
		`{"equalTo":5}`,
		`{"equalTo":"x","caseInsensitive":"yes"}`,
		`{"matches":"("}`,
		`{"absent":"yes"}`,
		`{"and":"x"}`,
		`{"and":[{"bogus":1}]}`,
		`{"not":"x"}`,
		`{"not":{"bogus":1}}`,
		`{"equalToNumber":"five"}`,
		`{"before":"not a date"}`,
		`{"before":"now +x days"}`,
		`{"before":"now +1 fortnights"}`,
		`{"before":"now 1 2 3"}`,
		`{"before":5}`,
		`{"before":"now","truncateExpected":"first day of eternity"}`,
		`{"before":"now","truncateActual":7}`,
		`{"before":"now","actualFormat":7}`,
		`{"before":"now","expectedOffset":"x"}`,
		`{"before":"now","expectedOffset":1,"expectedOffsetUnit":7}`,
	} {
		if _, err := CompileString(vp(t, p), opts()); err == nil {
			t.Errorf("CompileString(%s) succeeded, want an error", p)
		}
	}
}

func TestTruncations(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 10, 27, 15, 45, 30, 0, time.UTC)
	tests := map[string]time.Time{
		"first minute of hour":    time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC),
		"first hour of day":       time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		"first day of month":      time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		"first day of next month": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		"last day of month":       time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		"first day of year":       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"first day of next year":  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		"last day of year":        time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	for how, want := range tests {
		got, ok := truncate(base, how)
		if !ok || !got.Equal(want) {
			t.Errorf("truncate(%s) = %v, %v, want %v", how, got, ok, want)
		}
	}
	for unit, want := range map[string]time.Time{
		"seconds": base.Add(2 * time.Second), "minute": base.Add(2 * time.Minute), "hours": base.Add(2 * time.Hour),
		"months": base.AddDate(0, 2, 0), "years": base.AddDate(2, 0, 0),
	} {
		if got, ok := addOffset(base, 2, unit); !ok || !got.Equal(want) {
			t.Errorf("addOffset(2 %s) = %v, want %v", unit, got, want)
		}
	}
}

func TestCompileMulti(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		values  []string
		want    bool
	}{
		{"single matches any value", `{"equalTo":"b"}`, []string{"a", "b"}, true},
		{"single no value", `{"equalTo":"b"}`, nil, false},
		{"absent with no values", `{"absent":true}`, nil, true},
		{"absent with values", `{"absent":true}`, []string{"a"}, false},
		{"includes", `{"includes":[{"equalTo":"a"},{"equalTo":"c"}]}`, []string{"a", "b", "c"}, true},
		{"includes missing", `{"includes":[{"equalTo":"a"},{"equalTo":"z"}]}`, []string{"a", "b"}, false},
		{"hasExactly", `{"hasExactly":[{"equalTo":"a"},{"equalTo":"b"}]}`, []string{"b", "a"}, true},
		{"hasExactly extra value", `{"hasExactly":[{"equalTo":"a"}]}`, []string{"a", "b"}, false},
		{"hasExactly unmatched value", `{"hasExactly":[{"contains":"a"},{"contains":"a"}]}`, []string{"a", "b"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := CompileMulti(vp(t, tt.pattern), opts())
			if err != nil {
				t.Fatalf("CompileMulti(%s) error: %v", tt.pattern, err)
			}
			if got := m.MatchValues(tt.values).Matched; got != tt.want {
				t.Errorf("MatchValues(%v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
	for _, bad := range []string{`{"hasExactly":"x"}`, `{"includes":[{"nope":1}]}`, `{"nope":1}`} {
		if _, err := CompileMulti(vp(t, bad), opts()); err == nil {
			t.Errorf("CompileMulti(%s) succeeded, want an error", bad)
		}
	}
}

func TestTextDistance(t *testing.T) {
	t.Parallel()
	if d := textDistance("abc", "abc"); d != 0 {
		t.Errorf("textDistance(equal) = %v, want 0", d)
	}
	if d := textDistance("", ""); d != 0 {
		t.Errorf("textDistance(empty) = %v, want 0", d)
	}
	near, far := textDistance("/payments/1", "/payments/2"), textDistance("/payments/1", "/x")
	if near >= far {
		t.Errorf("textDistance: near %v should be below far %v", near, far)
	}
	if javaLayout("yyyy-MM-dd'T'HH:mm:ss") != "2006-01-02T15:04:05" {
		t.Errorf("javaLayout = %q", javaLayout("yyyy-MM-dd'T'HH:mm:ss"))
	}
}
