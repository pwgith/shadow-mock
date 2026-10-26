package match

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pwgith/shadow-mock/internal/model"
)

// dateMatcher implements before, after and equalToDateTime (FR-2.4).
type dateMatcher struct {
	op            string
	fixed         time.Time
	relative      bool
	offset        int
	unit          string
	actualLayout  string
	truncExpected string
	truncActual   string
	opts          Options
}

func compileDate(op string, p model.ValuePattern, o Options) (StringMatcher, error) {
	m := &dateMatcher{op: op, opts: o}
	var expected string
	if err := json.Unmarshal(p[op], &expected); err != nil {
		return nil, fmt.Errorf("%s must be a string: %w", op, err)
	}
	if err := m.parseExpected(expected, p); err != nil {
		return nil, err
	}
	for field, dst := range map[string]*string{"truncateExpected": &m.truncExpected, "truncateActual": &m.truncActual} {
		if raw, ok := p[field]; ok {
			if err := json.Unmarshal(raw, dst); err != nil {
				return nil, fmt.Errorf("%s must be a string: %w", field, err)
			}
			if _, ok := truncate(time.Time{}, *dst); !ok {
				return nil, fmt.Errorf("unknown %s %q", field, *dst)
			}
		}
	}
	if raw, ok := p["actualFormat"]; ok {
		var f string
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("actualFormat must be a string: %w", err)
		}
		m.actualLayout = javaLayout(f)
	}
	return m, nil
}

// parseRelative parses "now", "now +3 days" or "now -1 hours".
func (m *dateMatcher) parseRelative(expected string) error {
	m.relative = true
	rest := strings.Fields(strings.TrimSpace(expected)[3:])
	switch len(rest) {
	case 0:
		return nil
	case 2:
		n, err := strconv.Atoi(strings.TrimPrefix(rest[0], "+"))
		if err != nil {
			return fmt.Errorf("invalid relative date %q", expected)
		}
		m.offset, m.unit = n, rest[1]
		return nil
	default:
		return fmt.Errorf("invalid relative date %q", expected)
	}
}

func (m *dateMatcher) parseExpected(expected string, p model.ValuePattern) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(expected)), "now") {
		if err := m.parseRelative(expected); err != nil {
			return err
		}
	} else {
		t, ok := parseTime(expected, "")
		if !ok {
			return fmt.Errorf("invalid date %q", expected)
		}
		m.fixed = t
	}
	if raw, ok := p["expectedOffset"]; ok {
		if err := json.Unmarshal(raw, &m.offset); err != nil {
			return fmt.Errorf("expectedOffset must be an integer: %w", err)
		}
		m.unit = "days"
		if u, ok := p["expectedOffsetUnit"]; ok {
			if err := json.Unmarshal(u, &m.unit); err != nil {
				return fmt.Errorf("expectedOffsetUnit must be a string: %w", err)
			}
		}
	}
	if _, ok := addOffset(time.Time{}, m.offset, m.unit); !ok {
		return fmt.Errorf("unknown offset unit %q", m.unit)
	}
	return nil
}

func (m *dateMatcher) expected() time.Time {
	base := m.fixed
	if m.relative {
		base = m.opts.now().UTC()
	}
	t, _ := addOffset(base, m.offset, m.unit)
	if m.truncExpected != "" {
		t, _ = truncate(t, m.truncExpected)
	}
	return t
}

// MatchString implements StringMatcher.
func (m *dateMatcher) MatchString(v string, present bool) Result {
	if !present {
		return miss()
	}
	actual, ok := parseTime(v, m.actualLayout)
	if !ok {
		return miss()
	}
	if m.truncActual != "" {
		actual, _ = truncate(actual, m.truncActual)
	}
	want := m.expected()
	switch m.op {
	case "before":
		return boolResult(actual.Before(want))
	case "after":
		return boolResult(actual.After(want))
	default:
		return boolResult(actual.Equal(want))
	}
}

func addOffset(t time.Time, n int, unit string) (time.Time, bool) {
	switch strings.TrimSuffix(strings.ToLower(unit), "s") {
	case "":
		return t, n == 0
	case "second":
		return t.Add(time.Duration(n) * time.Second), true
	case "minute":
		return t.Add(time.Duration(n) * time.Minute), true
	case "hour":
		return t.Add(time.Duration(n) * time.Hour), true
	case "day":
		return t.AddDate(0, 0, n), true
	case "month":
		return t.AddDate(0, n, 0), true
	case "year":
		return t.AddDate(n, 0, 0), true
	default:
		return t, false
	}
}

// truncate applies a WireMock truncation such as "first day of month".
func truncate(t time.Time, how string) (time.Time, bool) {
	y, mo, d := t.Date()
	loc := t.Location()
	switch strings.ToLower(how) {
	case "first minute of hour":
		return time.Date(y, mo, d, t.Hour(), 0, 0, 0, loc), true
	case "first hour of day":
		return time.Date(y, mo, d, 0, 0, 0, 0, loc), true
	case "first day of month":
		return time.Date(y, mo, 1, 0, 0, 0, 0, loc), true
	case "first day of next month":
		return time.Date(y, mo+1, 1, 0, 0, 0, 0, loc), true
	case "last day of month":
		return time.Date(y, mo+1, 0, 0, 0, 0, 0, loc), true
	case "first day of year":
		return time.Date(y, 1, 1, 0, 0, 0, 0, loc), true
	case "first day of next year":
		return time.Date(y+1, 1, 1, 0, 0, 0, 0, loc), true
	case "last day of year":
		return time.Date(y, 12, 31, 0, 0, 0, 0, loc), true
	default:
		return t, false
	}
}

// parseTime parses an actual or expected date: with layout if given, otherwise as ISO 8601,
// an HTTP date or a plain date.
func parseTime(s, layout string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if layout != "" {
		t, err := time.Parse(layout, s)
		return t, err == nil
	}
	for _, l := range []string{
		time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04",
		time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC, "2006-01-02",
	} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// javaLayout converts a Java DateTimeFormatter pattern (as WireMock uses) to a Go layout.
func javaLayout(p string) string {
	r := strings.NewReplacer(
		"yyyy", "2006", "yy", "06", "MMMM", "January", "MMM", "Jan", "MM", "01", "M", "1",
		"dd", "02", "d", "2", "HH", "15", "hh", "03", "mm", "04", "ss", "05",
		"SSS", "000", "a", "PM", "EEEE", time.Monday.String(), "EEE", "Mon", "XXX", "Z07:00", "Z", "-0700",
		"'T'", "T", "''", "'",
	)
	return r.Replace(p)
}
