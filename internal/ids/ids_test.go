package ids_test

import (
	"regexp"
	"testing"

	"github.com/pwgith/shadow-mock/internal/ids"
)

func TestSessionKey(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^smk_[0-9A-HJKMNP-TV-Z]{26}$`)
	seen := map[string]bool{}
	for range 1000 {
		k := ids.NewSessionKey("")
		if !re.MatchString(k) {
			t.Fatalf("NewSessionKey() = %q, want smk_ and 26 Crockford characters", k)
		}
		if seen[k] {
			t.Fatalf("NewSessionKey() repeated %q", k)
		}
		seen[k] = true
	}
	if k := ids.NewSessionKey("test_"); len(k) != len("test_")+26 || k[:5] != "test_" {
		t.Errorf("NewSessionKey(test_) = %q", k)
	}
}

func TestSessionID(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^ses_[0-9A-HJKMNP-TV-Z]{26}$`)
	if id := ids.NewSessionID(); !re.MatchString(id) || id == ids.NewSessionID() {
		t.Errorf("NewSessionID() = %q", id)
	}
}

func TestURLMockID(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^[a-z2-7]+$`)
	for _, tt := range []struct{ in, want int }{{0, 16}, {-3, 16}, {16, 16}, {24, 24}, {7, 7}} {
		id := ids.NewURLMockID(tt.in)
		if len(id) != tt.want || !re.MatchString(id) {
			t.Errorf("NewURLMockID(%d) = %q, want %d characters of lower-case base32", tt.in, id, tt.want)
		}
	}
	seen := map[string]bool{}
	for range 1000 {
		id := ids.NewURLMockID(16)
		if seen[id] {
			t.Fatalf("NewURLMockID repeated %q", id)
		}
		seen[id] = true
	}
}

func TestUUID(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if u := ids.NewUUID(); !re.MatchString(u) || u == ids.NewUUID() {
		t.Errorf("NewUUID() = %q, want a random version 4 UUID", u)
	}
}

func TestIsSessionID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{ids.NewSessionID(), true},
		{ids.NewURLMockID(16), true},
		{"ses_00000000000000000000000000", true},
		{"ses_0000000000000000000000000", false},  // 25 characters
		{"ses_0000000000000000000000000I", false}, // I is not Crockford
		{"abcdefghijklmnop", true},                // 16 base32 characters
		{"abcdefghijklmno1", false},               // 1 is not in the lower-case alphabet
		{"abcdefghijklmnopq", false},              // 17 characters
		{"", false},
	}
	for _, tt := range tests {
		if got := ids.IsSessionID(tt.in); got != tt.want {
			t.Errorf("IsSessionID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
