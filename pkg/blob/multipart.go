package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ErrNoSuchUpload means the multipart upload does not exist (never created, aborted, or already completed).
var ErrNoSuchUpload = errors.New("blob: no such multipart upload")

func isNoSuchUpload(err error) bool {
	var nsu *types.NoSuchUpload
	return errors.As(err, &nsu)
}

// CreateMultipart starts a multipart upload and returns its upload id.
func (s *Store) CreateMultipart(ctx context.Context, key string, o PutOptions) (string, error) {
	in := &s3.CreateMultipartUploadInput{Bucket: &s.cfg.Bucket, Key: &key, ContentType: nilIfEmpty(o.ContentType), Metadata: o.Metadata}
	if len(o.Tags) > 0 {
		q := url.Values{}
		for k, v := range o.Tags {
			q.Set(k, v)
		}
		in.Tagging = aws.String(q.Encode())
	}
	out, err := s.client.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", fmt.Errorf("blob: create multipart %s: %w", key, err)
	}
	return aws.ToString(out.UploadId), nil
}

// PresignPart returns a URL a client can PUT part bytes to (valid for ttl against the public endpoint). With a non-empty
// checksumSHA256 (base64) the URL is signed with that x-amz-checksum-sha256, so S3 rejects a part with other bytes.
func (s *Store) PresignPart(ctx context.Context, key, uploadID string, part int32, ttl time.Duration, checksumSHA256 string) (string, error) {
	in := &s3.UploadPartInput{Bucket: &s.cfg.Bucket, Key: &key, UploadId: &uploadID, PartNumber: &part}
	if checksumSHA256 != "" {
		in.ChecksumSHA256 = &checksumSHA256
	}
	r, err := s.presign.PresignUploadPart(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob: presign part %d of %s: %w", part, key, err)
	}
	return r.URL, nil
}

// Part is an uploaded part as S3 reports it.
type Part struct {
	Number int32
	Size   int64
	ETag   string
}

// ListParts returns every part S3 holds for the upload, in part order. A missing upload is ErrNoSuchUpload.
func (s *Store) ListParts(ctx context.Context, key, uploadID string) ([]Part, error) {
	var out []Part
	p := s3.NewListPartsPaginator(s.client, &s3.ListPartsInput{Bucket: &s.cfg.Bucket, Key: &key, UploadId: &uploadID})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isNoSuchUpload(err) {
				return nil, ErrNoSuchUpload
			}
			return nil, fmt.Errorf("blob: list parts %s: %w", key, err)
		}
		for _, part := range page.Parts {
			out = append(out, Part{Number: aws.ToInt32(part.PartNumber), Size: aws.ToInt64(part.Size), ETag: aws.ToString(part.ETag)})
		}
	}
	return out, nil
}

// CompleteMultipart assembles the object from the listed parts (number and ETag).
func (s *Store) CompleteMultipart(ctx context.Context, key, uploadID string, parts []Part) error {
	cp := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		num := p.Number
		etag := p.ETag
		cp[i] = types.CompletedPart{PartNumber: &num, ETag: &etag}
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: &s.cfg.Bucket, Key: &key, UploadId: &uploadID, MultipartUpload: &types.CompletedMultipartUpload{Parts: cp}})
	if err != nil {
		if isNoSuchUpload(err) {
			return ErrNoSuchUpload
		}
		return fmt.Errorf("blob: complete multipart %s: %w", key, err)
	}
	return nil
}

// AbortMultipart discards an upload and its parts. Aborting a missing upload is not an error.
func (s *Store) AbortMultipart(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: &s.cfg.Bucket, Key: &key, UploadId: &uploadID})
	if err != nil && !isNoSuchUpload(err) {
		return fmt.Errorf("blob: abort multipart %s: %w", key, err)
	}
	return nil
}

// MultipartUpload is an in-progress upload.
type MultipartUpload struct {
	Key      string
	UploadID string
}

// ListMultipartUploads lists the in-progress uploads under a key prefix.
func (s *Store) ListMultipartUploads(ctx context.Context, prefix string) ([]MultipartUpload, error) {
	var out []MultipartUpload
	var keyMarker, uploadMarker *string
	for {
		page, err := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: &s.cfg.Bucket, Prefix: &prefix, KeyMarker: keyMarker, UploadIdMarker: uploadMarker})
		if err != nil {
			return nil, fmt.Errorf("blob: list multipart uploads: %w", err)
		}
		for _, u := range page.Uploads {
			out = append(out, MultipartUpload{Key: aws.ToString(u.Key), UploadID: aws.ToString(u.UploadId)})
		}
		if !aws.ToBool(page.IsTruncated) {
			return out, nil
		}
		keyMarker, uploadMarker = page.NextKeyMarker, page.NextUploadIdMarker
	}
}

// StreamOptions tune PutStream.
type StreamOptions struct {
	PutOptions
	// PartSize and Concurrency default to 8 MiB and 4: the memory bound of one stream is about their product.
	PartSize    int64
	Concurrency int
}

// PutStream writes a reader of unknown length with a server-side multipart upload (SDK upload manager). If the reader
// fails or ctx is cancelled the multipart upload is aborted, so no parts are left behind.
func (s *Store) PutStream(ctx context.Context, key string, body io.Reader, o StreamOptions) error {
	if o.PartSize <= 0 {
		o.PartSize = 8 << 20
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	in := &s3.PutObjectInput{Bucket: &s.cfg.Bucket, Key: &key, Body: body, ContentType: nilIfEmpty(o.ContentType), Metadata: o.Metadata}
	if len(o.Tags) > 0 {
		q := url.Values{}
		for k, v := range o.Tags {
			q.Set(k, v)
		}
		in.Tagging = aws.String(q.Encode())
	}
	// The upload manager is deprecated in favour of feature/s3/transfermanager, which has no stable release yet.
	up := manager.NewUploader(s.client, func(u *manager.Uploader) { //nolint:staticcheck
		u.PartSize = o.PartSize
		u.Concurrency = o.Concurrency
		u.LeavePartsOnError = false
	})
	if _, err := up.Upload(ctx, in); err != nil { //nolint:staticcheck
		return fmt.Errorf("blob: stream %s: %w", key, err)
	}
	return nil
}

// GetRange reads up to n bytes from the start of an object (content sniffing).
func (s *Store) GetRange(ctx context.Context, key string, n int64) ([]byte, error) {
	rng := fmt.Sprintf("bytes=0-%d", n-1)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key, Range: &rng})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob: get range %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// Open streams an object (verification reads it end to end without holding it in memory).
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob: open %s: %w", key, err)
	}
	return out.Body, nil
}

// PresignDownload is PresignGet with Content-Disposition: attachment, so a browser saves the file instead of rendering it.
func (s *Store) PresignDownload(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	disp := "attachment; filename*=UTF-8''" + url.PathEscape(filename)
	r, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key, ResponseContentDisposition: &disp}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob: presign download %s: %w", key, err)
	}
	return r.URL, nil
}

// Keys lists object keys (with their modification time) under a prefix, for the orphan reconciler.
func (s *Store) Keys(ctx context.Context, prefix string, fn func(key string, modified time.Time) error) error {
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.cfg.Bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("blob: list %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			if err := fn(aws.ToString(o.Key), aws.ToTime(o.LastModified)); err != nil {
				return err
			}
		}
	}
	return nil
}
