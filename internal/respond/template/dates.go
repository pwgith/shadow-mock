package template

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// javaLayout converts a Java DateTimeFormatter pattern, which WireMock uses, to a Go layout.
func javaLayout(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); {
		c := p[i]
		if c == '\'' {
			j := strings.IndexByte(p[i+1:], '\'')
			if j < 0 {
				b.WriteString(p[i+1:])
				break
			}
			if j == 0 { // two quotes in a row are one literal quote
				b.WriteByte('\'')
			} else {
				b.WriteString(p[i+1 : i+1+j])
			}
			i += j + 2
			continue
		}
		if !isLetter(c) {
			b.WriteByte(c)
			i++
			continue
		}
		n := 1
		for i+n < len(p) && p[i+n] == c {
			n++
		}
		b.WriteString(letterLayout(c, n))
		i += n
	}
	return b.String()
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func letterLayout(c byte, n int) string {
	switch c {
	case 'y', 'u':
		if n == 2 {
			return "06"
		}
		return "2006"
	case 'M', 'L':
		return pick(n, "1", "01", "Jan", "January")
	case 'd':
		return pick(n, "2", "02", "02", "02")
	case 'H':
		return "15"
	case 'h':
		return pick(n, "3", "03", "03", "03")
	case 'm':
		return pick(n, "4", "04", "04", "04")
	case 's':
		return pick(n, "5", "05", "05", "05")
	case 'S':
		return strings.Repeat("0", n)
	case 'a':
		return "PM"
	case 'E':
		return pick(n, "Mon", "Mon", "Mon", time.Monday.String())
	case 'X':
		return pick(n, "Z07", "Z0700", "Z07:00", "Z07:00")
	case 'x':
		return pick(n, "-07", "-0700", "-07:00", "-07:00")
	case 'Z':
		return "-0700"
	case 'z':
		return "MST"
	default:
		return strings.Repeat(string(c), n)
	}
}

// pick returns the layout for a run of n pattern letters.
func pick(n int, one, two, three, four string) string {
	switch {
	case n <= 1:
		return one
	case n == 2:
		return two
	case n == 3:
		return three
	default:
		return four
	}
}

// formatTime formats t with a Java pattern, or with the special names epoch (milliseconds)
// and unix (seconds). An empty format gives ISO 8601.
func formatTime(t time.Time, format string) string {
	switch format {
	case "":
		return t.Format("2006-01-02T15:04:05Z07:00")
	case "epoch":
		return strconv.FormatInt(t.UnixMilli(), 10)
	case "unix":
		return strconv.FormatInt(t.Unix(), 10)
	default:
		return t.Format(javaLayout(format))
	}
}

// parseTime parses text with a Java pattern, or with unix (seconds) or epoch (milliseconds). Without
// a format it reads ISO 8601, then the RFC 1123 and asctime forms of HTTP dates.
func parseTime(text, format string) (time.Time, error) {
	switch format {
	case "":
		for _, layout := range []string{time.RFC3339Nano, time.RFC1123, time.RFC1123Z, time.ANSIC} {
			if t, err := time.Parse(layout, text); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("parse date %q: not an ISO 8601, RFC 1123 or asctime date", text)
	case "unix", "epoch":
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse date %q as %s: %w", text, format, err)
		}
		if format == "unix" {
			return time.Unix(n, 0).UTC(), nil
		}
		return time.UnixMilli(n).UTC(), nil
	}
	t, err := time.Parse(javaLayout(format), text)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date %q with %q: %w", text, format, err)
	}
	return t, nil
}

// offsetTime applies a WireMock offset such as "2 days", "-3 hours" or "1 months".
func offsetTime(t time.Time, offset string) (time.Time, error) {
	offset = strings.TrimSpace(offset)
	if offset == "" {
		return t, nil
	}
	fields := strings.Fields(offset)
	if len(fields) != 2 {
		return t, fmt.Errorf("offset %q must look like \"2 days\"", offset)
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return t, fmt.Errorf("offset %q: %w", offset, err)
	}
	switch strings.TrimSuffix(strings.ToLower(fields[1]), "s") {
	case "second":
		return t.Add(time.Duration(n) * time.Second), nil
	case "minute":
		return t.Add(time.Duration(n) * time.Minute), nil
	case "hour":
		return t.Add(time.Duration(n) * time.Hour), nil
	case "day":
		return t.AddDate(0, 0, n), nil
	case "week":
		return t.AddDate(0, 0, 7*n), nil
	case "month":
		return t.AddDate(0, n, 0), nil
	case "year":
		return t.AddDate(n, 0, 0), nil
	default:
		return t, fmt.Errorf("offset %q has an unknown unit", offset)
	}
}

// zone resolves a time zone name, defaulting to UTC.
func zone(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("time zone %q: %w", name, err)
	}
	return loc, nil
}

// truncations returns the specs WireMock's truncateDate accepts, in lower case. The "last" ones give
// the start of the last day.
func truncations() map[string]func(time.Time) time.Time {
	return map[string]func(time.Time) time.Time{
		"first second of minute": func(t time.Time) time.Time { return t.Truncate(time.Minute) },
		"first minute of hour":   func(t time.Time) time.Time { return t.Truncate(time.Hour) },
		"first hour of day":      startOfDay,
		"first day of month":     startOfMonth,
		"first day of next month": func(t time.Time) time.Time {
			return startOfMonth(t).AddDate(0, 1, 0)
		},
		"last day of month": func(t time.Time) time.Time {
			return startOfMonth(t).AddDate(0, 1, -1)
		},
		"first day of year": startOfYear,
		"first day of next year": func(t time.Time) time.Time {
			return startOfYear(t).AddDate(1, 0, 0)
		},
		"last day of year": func(t time.Time) time.Time {
			return startOfYear(t).AddDate(1, 0, -1)
		},
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func startOfYear(t time.Time) time.Time {
	return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, t.Location())
}

// truncate implements truncateDate: spec is one of the keys of truncations(), in any case.
func truncate(t time.Time, spec string) (time.Time, error) {
	f, ok := truncations()[strings.ToLower(strings.Join(strings.Fields(spec), " "))]
	if !ok {
		return t, fmt.Errorf("cannot truncate to %q; use for example \"first day of month\"", spec)
	}
	return f(t), nil
}
