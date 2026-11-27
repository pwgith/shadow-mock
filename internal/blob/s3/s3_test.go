package s3_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/pwgith/shadow-mock/internal/blob"
	"github.com/pwgith/shadow-mock/internal/blob/blobtest"
	"github.com/pwgith/shadow-mock/internal/blob/s3"
	"github.com/pwgith/shadow-mock/internal/clock"
)

// fakeS3 starts an in-process S3 server with one empty bucket.
func fakeS3(t *testing.T) *httptest.Server {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket("mocks"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	return srv
}

func TestS3PassesTheSuite(t *testing.T) {
	t.Parallel()
	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()
		c, err := s3.New(s3.Config{Endpoint: fakeS3(t).URL, Bucket: "mocks", AccessKey: "k", SecretKey: "s", PathStyle: true}, clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}

// An endpoint with a path of its own, as Supabase Storage has (https://<ref>.storage.supabase.co/storage/v1/s3),
// keeps it in front of the bucket and key.
func TestS3KeepsTheEndpointPath(t *testing.T) {
	t.Parallel()
	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()
		backend := s3mem.New()
		if err := backend.CreateBucket("mocks"); err != nil {
			t.Fatal(err)
		}
		fake := gofakes3.New(backend).Server()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/storage/v1/s3/mocks") {
				http.Error(w, "outside the endpoint path: "+r.URL.Path, http.StatusNotFound)
				return
			}
			http.StripPrefix("/storage/v1/s3", fake).ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		c, err := s3.New(s3.Config{Endpoint: srv.URL + "/storage/v1/s3/", Bucket: "mocks", AccessKey: "k", SecretKey: "s", PathStyle: true}, clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		return c
	})
}
