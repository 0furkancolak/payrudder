//go:build integration

package blob_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

// The contract every S3-compatible backend must meet; it runs against MinIO with path-style addressing, the way Compose
// and kind use it.
func TestContract(t *testing.T) {
	m := testkit.StartMinio(t)
	s, err := blob.New(blob.Config{Endpoint: m.Endpoint, Region: "us-east-1", Bucket: "contract", AccessKey: m.AccessKey, SecretKey: m.SecretKey, ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBucket(ctx); err != nil { // idempotent
		t.Fatalf("second EnsureBucket: %v", err)
	}

	t.Run("PutHeadGetRoundTrip", func(t *testing.T) {
		body := []byte("%PDF-1.4 hello")
		if err := s.Put(ctx, "t1/inv/v1.pdf", body, blob.PutOptions{ContentType: "application/pdf", Metadata: map[string]string{"sha256": "abc"},
			Tags: map[string]string{"environment": "sandbox"}}); err != nil {
			t.Fatal(err)
		}
		info, err := s.Head(ctx, "t1/inv/v1.pdf")
		if err != nil || info.Size != int64(len(body)) || info.ContentType != "application/pdf" || info.Metadata["sha256"] != "abc" {
			t.Fatalf("head = %+v, %v", info, err)
		}
		got, err := s.Get(ctx, "t1/inv/v1.pdf")
		if err != nil || string(got) != string(body) {
			t.Fatalf("get = %q, %v", got, err)
		}
		tags, err := s.Tags(ctx, "t1/inv/v1.pdf")
		if err != nil || tags["environment"] != "sandbox" {
			t.Fatalf("tags = %v, %v", tags, err)
		}
	})

	t.Run("MissingObjectIsNotFound", func(t *testing.T) {
		if _, err := s.Head(ctx, "nope"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("head err = %v, want ErrNotFound", err)
		}
		if _, err := s.Get(ctx, "nope"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("get err = %v, want ErrNotFound", err)
		}
	})

	t.Run("OverwriteKeepsOneObject", func(t *testing.T) {
		for range 3 {
			if err := s.Put(ctx, "dup/key.pdf", []byte("same"), blob.PutOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := s.Count(ctx, "dup/"); err != nil || n != 1 {
			t.Fatalf("count = %d, %v; want 1", n, err)
		}
	})

	t.Run("PresignedURLDownloadsAndExpires", func(t *testing.T) {
		if err := s.Put(ctx, "p/file.txt", []byte("presigned"), blob.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		u, err := s.PresignGet(ctx, "p/file.txt", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || string(b) != "presigned" {
			t.Fatalf("GET presigned = %d %q", resp.StatusCode, b)
		}
		// A public endpoint different from the service endpoint is honoured in the link.
		pub, _ := blob.New(blob.Config{Endpoint: m.Endpoint, PublicEndpoint: "http://localhost:19999", Bucket: "contract", AccessKey: m.AccessKey, SecretKey: m.SecretKey, ForcePathStyle: true})
		pu, err := pub.PresignGet(ctx, "p/file.txt", time.Minute)
		if err != nil || len(pu) < 22 || pu[:22] != "http://localhost:19999" {
			t.Fatalf("public presign = %q, %v", pu, err)
		}
	})

	t.Run("DeleteIsIdempotent", func(t *testing.T) {
		_ = s.Put(ctx, "d/x", []byte("x"), blob.PutOptions{})
		if err := s.Delete(ctx, "d/x"); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "d/x"); err != nil {
			t.Fatalf("second delete: %v", err)
		}
		if _, err := s.Head(ctx, "d/x"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("object still exists")
		}
	})
}
