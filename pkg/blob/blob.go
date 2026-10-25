// Package blob is Payrudder's object storage client. It speaks only the S3 API (ADR 0013): MinIO in Compose and kind,
// any S3-compatible service elsewhere, so switching is configuration, not code.
package blob

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Config configures a Store. It mirrors the S3_* environment variables of the services.
type Config struct {
	// Endpoint is the S3 API URL the service talks to (http://minio:9000). Empty means AWS.
	Endpoint string
	// PublicEndpoint is the URL clients use for presigned links (http://localhost:42901). Empty means Endpoint.
	PublicEndpoint string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	// ForcePathStyle is required by MinIO.
	ForcePathStyle bool
}

// Store is an S3 bucket.
type Store struct {
	cfg     Config
	client  *s3.Client
	presign *s3.PresignClient
}

func newClient(cfg Config, endpoint string) *s3.Client {
	return s3.New(s3.Options{
		Region:       cmp.Or(cfg.Region, "us-east-1"),
		BaseEndpoint: nilIfEmpty(endpoint),
		UsePathStyle: cfg.ForcePathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		// MinIO and other S3-compatible servers don't all accept the SDK's default flexible checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
}

// New returns a Store. It does not contact the server.
func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("blob: bucket is required")
	}
	pub := cfg.PublicEndpoint
	if pub == "" {
		pub = cfg.Endpoint
	}
	return &Store{cfg: cfg, client: newClient(cfg, cfg.Endpoint), presign: s3.NewPresignClient(newClient(cfg, pub))}, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// PutOptions are the optional parts of a Put.
type PutOptions struct {
	ContentType string
	// Metadata is stored as user metadata (x-amz-meta-*); keys are lower-cased by S3.
	Metadata map[string]string
	// Tags are object tags (lifecycle rules match on them, e.g. environment=sandbox).
	Tags map[string]string
}

// Put writes an object, replacing any existing one.
func (s *Store) Put(ctx context.Context, key string, body []byte, o PutOptions) error {
	in := &s3.PutObjectInput{
		Bucket: &s.cfg.Bucket, Key: &key, Body: bytes.NewReader(body), ContentType: nilIfEmpty(o.ContentType), Metadata: o.Metadata,
	}
	if len(o.Tags) > 0 {
		q := url.Values{}
		for k, v := range o.Tags {
			q.Set(k, v)
		}
		in.Tagging = aws.String(q.Encode())
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("blob: put %s: %w", key, err)
	}
	return nil
}

// Info describes an object.
type Info struct {
	Size        int64
	ContentType string
	Metadata    map[string]string
	ETag        string
	Modified    time.Time
}

// ErrNotFound means the object does not exist.
var ErrNotFound = errors.New("blob: object not found")

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}
	return false
}

// Head returns the object's metadata, or ErrNotFound.
func (s *Store) Head(ctx context.Context, key string) (Info, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err != nil {
		if isNotFound(err) {
			return Info{}, ErrNotFound
		}
		return Info{}, fmt.Errorf("blob: head %s: %w", key, err)
	}
	info := Info{Size: aws.ToInt64(out.ContentLength), ContentType: aws.ToString(out.ContentType), Metadata: out.Metadata, ETag: aws.ToString(out.ETag)}
	if out.LastModified != nil {
		info.Modified = *out.LastModified
	}
	return info, nil
}

// Get reads a whole object, or returns ErrNotFound.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob: get %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// Tags returns the object's tags.
func (s *Store) Tags(ctx context.Context, key string) (map[string]string, error) {
	out, err := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("blob: tags %s: %w", key, err)
	}
	tags := make(map[string]string, len(out.TagSet))
	for _, t := range out.TagSet {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return tags, nil
}

// Delete removes an object (deleting a missing object is not an error).
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.cfg.Bucket, Key: &key}); err != nil {
		return fmt.Errorf("blob: delete %s: %w", key, err)
	}
	return nil
}

// Count lists the keys under a prefix (used by tests and housekeeping).
func (s *Store) Count(ctx context.Context, prefix string) (int, error) {
	n := 0
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.cfg.Bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return 0, fmt.Errorf("blob: list %s: %w", prefix, err)
		}
		n += len(page.Contents)
	}
	return n, nil
}

// PresignGet returns a time-limited download URL for the object, valid against the public endpoint.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	r, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob: presign %s: %w", key, err)
	}
	return r.URL, nil
}

// EnsureBucket creates the bucket when it does not exist (tests and local setups; deployments create buckets with
// tooling).
func (s *Store) EnsureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.cfg.Bucket}); err == nil {
		return nil
	}
	if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.cfg.Bucket}); err != nil {
		var owned *types.BucketAlreadyOwnedByYou
		if errors.As(err, &owned) {
			return nil
		}
		return fmt.Errorf("blob: create bucket: %w", err)
	}
	return nil
}
