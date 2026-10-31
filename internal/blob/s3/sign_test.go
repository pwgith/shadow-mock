package s3

import (
	"net/http"
	"testing"
	"time"

	"github.com/pwgith/shadow-mock/internal/clock"
)

// TestSignMatchesAWSExample checks the signature against the worked example in AWS's documentation, "Example:
// GET Object" in "Signature Calculations for the Authorization Header".
func TestSignMatchesAWSExample(t *testing.T) {
	t.Parallel()
	c, err := New(Config{
		Endpoint: "https://s3.amazonaws.com", Region: "us-east-1", Bucket: "examplebucket",
		AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	const empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	c.sign(req, empty, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request," +
		" SignedHeaders=host;range;x-amz-content-sha256;x-amz-date," +
		" Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("authorization:\n got %s\nwant %s", got, want)
	}
}

func TestEscape(t *testing.T) {
	t.Parallel()
	if got := escapePath("files/acme/a+b=c@d"); got != "files/acme/a%2Bb%3Dc%40d" {
		t.Errorf("escapePath = %s", got)
	}
}
