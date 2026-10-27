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

// parseTime parses text with a Java pattern, falling back to RFC 3339.
func parseTime(text, format string) (time.Time, error) {
	if format != "" {
		t, err := time.Parse(javaLayout(format), text)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse date %q with %q: %w", text, format, err)
		}
		return t, nil
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date %q: %w", text, err)
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

// truncate implements WireMock's truncateDate specs, such as "first day of month" (the start
// of the month) and "last day of month" (the start of the month's last day).
func truncate(t time.Time, spec string) (time.Time, error) {
	f := strings.Fields(spec)
	if len(f) != 4 || f[2] != "of" || (f[0] != "first" && f[0] != "last") {
		return t, fmt.Errorf("cannot truncate to %q; use for example \"first day of month\"", spec)
	}
	unit, container := f[1], f[3]
	start, err := floor(t, container)
	if err != nil {
		return t, err
	}
	if f[0] == "first" {
		return floor(start, unit)
	}
	var next time.Time
	switch container {
	case "hour":
		next = start.Add(time.Hour)
	case "day":
		next = start.AddDate(0, 0, 1)
	case "month":
		next = start.AddDate(0, 1, 0)
	default: // year
		next = start.AddDate(1, 0, 0)
	}
	return floor(next.Add(-time.Nanosecond), unit)
}

// floor rounds t down to the start of a unit.
func floor(t time.Time, unit string) (time.Time, error) {
	switch unit {
	case "minute":
		return t.Truncate(time.Minute), nil
	case "hour":
		return t.Truncate(time.Hour), nil
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()), nil
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()), nil
	case "year":
		return time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, t.Location()), nil
	default:
		return t, fmt.Errorf("unknown time unit %q", unit)
	}
}
