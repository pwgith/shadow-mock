package xmlguard_test

import (
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

func TestDepthOK(t *testing.T) {
	t.Parallel()
	nest := func(n int) []byte { return []byte(strings.Repeat("<a>", n) + "x" + strings.Repeat("</a>", n)) }
	for _, tt := range []struct {
		name string
		in   []byte
		want bool
	}{
		{"empty", nil, true},
		{"text", []byte("not xml"), true},
		{"shallow", nest(10), true},
		{"at the limit", nest(xmlguard.MaxDepth), true},
		{"past the limit", nest(xmlguard.MaxDepth + 1), false},
		{"a million levels", nest(1_000_000), false},
		{"wide but shallow", []byte("<r>" + strings.Repeat("<i/>", 50_000) + "</r>"), true},
		{"siblings do not add up", []byte(strings.Repeat("<a></a>", 5000)), true},
		{"truncated", []byte(strings.Repeat("<a>", 3000)), false},
		{"broken markup is left to the parser", []byte(strings.Repeat("<", 5000)), true},
	} {
		if got := xmlguard.DepthOK(tt.in); got != tt.want {
			t.Errorf("%s: DepthOK = %v, want %v", tt.name, got, tt.want)
		}
	}
}
