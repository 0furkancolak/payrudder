package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/store/sqlc"
)

// errTooLarge ends a stream that goes beyond its purpose's size limit.
var errTooLarge = errors.New("assets: file exceeds its size limit")

type uploadStream = grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]

// chunkReader presents the chunks of an UploadAsset stream as an io.Reader. A client that goes away surfaces as the
// stream's error, which aborts the S3 multipart upload.
type chunkReader struct {
	stream uploadStream
	buf    []byte
	max    int64
	n      int64
	eof    bool
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		msg, err := r.stream.Recv()
		if errors.Is(err, io.EOF) {
			r.eof = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}
		r.buf = msg.GetChunk()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	r.n += int64(n)
	if r.n > r.max {
		return n, errTooLarge
	}
	return n, nil
}

// proxiedUpload is one UploadAsset stream whose asset row exists.
type proxiedUpload struct {
	s           *Service
	ti          tenant.Info
	id          uuid.UUID
	key         string
	contentType string
	purpose     assetsv1.Purpose
	// ctx records how the upload ended; it outlives a cancelled stream context.
	ctx context.Context
}

// end records how the upload ended and returns the asset.
func (u *proxiedUpload) end(statusName, reason string) (sqlc.Asset, error) {
	var a sqlc.Asset
	err := u.s.tx(u.ctx, func(q *sqlc.Queries) error {
		if err := q.SetAssetOutcome(u.ctx, sqlc.SetAssetOutcomeParams{ID: u.id, Status: statusName, Reason: reason}); err != nil {
			return err
		}
		var err error
		a, err = loadAsset(u.ctx, q, u.id, false)
		return err
	})
	return a, err
}

// rejected ends the upload as REJECTED and answers the stream with the asset.
func (u *proxiedUpload) rejected(stream uploadStream, reason string) error {
	a, err := u.end("REJECTED", reason)
	if err != nil {
		return internal(err)
	}
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: assetProto(a)})
}

// complete turns the stored object into a READY asset.
func (u *proxiedUpload) complete(size int64, sha []byte) (sqlc.Asset, error) {
	var out sqlc.Asset
	err := u.s.tx(u.ctx, func(q *sqlc.Queries) error {
		a, err := loadAsset(u.ctx, q, u.id, true)
		if err != nil {
			return err
		}
		out, err = u.s.finish(u.ctx, q, u.ti, a, size, sha)
		return err
	})
	return out, err
}

// receiveHeader reads the first message of the stream, which must be the upload header.
func receiveHeader(stream uploadStream) (*assetsv1.UploadHeader, error) {
	first, err := stream.Recv()
	if err != nil || first.GetHeader() == nil {
		return nil, status.Error(codes.InvalidArgument, "the first message must be the upload header")
	}
	return first.GetHeader(), nil
}

// beginUpload validates the header, checks the quota for a declared size and records the UPLOADING asset.
func (s *Service) beginUpload(ctx context.Context, ti tenant.Info, h *assetsv1.UploadHeader) (*proxiedUpload, error) {
	name, err := policy.SanitizeFilename(h.GetFilename())
	if err != nil {
		return nil, failure(codes.InvalidArgument, policy.InvalidFilename, "the filename is unusable")
	}
	ct, v := s.pol.Check(h.GetPurpose(), h.GetContentType(), max(h.GetSize(), 0))
	if v != nil {
		return nil, fromViolation(v)
	}
	id := idgen.NewUUIDv7()
	key := objectKey(ti.ID, id)
	var declared *int64
	if h.GetSize() > 0 {
		sz := h.GetSize()
		declared = &sz
		used, err := s.bytesUsed(ctx)
		if err != nil {
			return nil, internal(err)
		}
		if used+sz > s.quota(ti) {
			return nil, failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the file does not fit in the tenant's storage quota")
		}
	}
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		return q.InsertProxiedAsset(ctx, sqlc.InsertProxiedAssetParams{
			ID: id, Purpose: policy.PurposeName(h.GetPurpose()), Filename: name, ContentType: ct, DeclaredSize: declared, ObjectKey: key,
		})
	})
	if err != nil {
		return nil, internal(err)
	}
	return &proxiedUpload{s: s, ti: ti, id: id, key: key, contentType: ct, purpose: h.GetPurpose(), ctx: context.WithoutCancel(ctx)}, nil
}

// readHead reads up to 512 bytes of the stream for content sniffing.
func readHead(r io.Reader) ([]byte, error) {
	head := make([]byte, 0, 512)
	for len(head) < 512 {
		buf := make([]byte, 512-len(head))
		n, err := r.Read(buf)
		head = append(head, buf[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return head, err
		}
	}
	return head, nil
}

// failedHead ends an upload whose head could not be read: too large is a rejection, anything else an abort.
func (u *proxiedUpload) failedHead(stream uploadStream, rerr error) error {
	a, err := u.end("REJECTED", policy.FileTooLarge)
	if errors.Is(rerr, errTooLarge) && err == nil {
		return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: assetProto(a)})
	}
	_, _ = u.end("ABORTED", "")
	return status.FromContextError(rerr).Err()
}

// store streams the body into S3 and returns the object's size. A rejection
// (the stream went past its limit) is answered on the stream and reported as done.
func (u *proxiedUpload) store(ctx context.Context, stream uploadStream, body io.Reader) (size int64, done bool, err error) {
	err = u.s.blob.PutStream(ctx, u.key, body, blob.StreamOptions{
		PutOptions: blob.PutOptions{ContentType: u.contentType, Tags: map[string]string{"purpose": policy.PurposeName(u.purpose)}},
	})
	if err != nil {
		// The upload manager aborted the multipart upload: no parts are left behind.
		if errors.Is(err, errTooLarge) {
			if a, eerr := u.end("REJECTED", policy.FileTooLarge); eerr == nil {
				return 0, true, stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: assetProto(a)})
			}
		}
		_, _ = u.end("ABORTED", "")
		if ctx.Err() != nil {
			return 0, false, status.FromContextError(ctx.Err()).Err()
		}
		return 0, false, status.Error(codes.Unavailable, "object storage unavailable")
	}
	info, err := u.s.blob.Head(u.ctx, u.key)
	if err != nil {
		_, _ = u.end("ABORTED", "")
		return 0, false, status.Error(codes.Unavailable, "object storage unavailable")
	}
	return info.Size, false, nil
}

// UploadAsset implements assetsv1.AssetsServiceServer: one file per stream, a header and then chunks. The first 512
// bytes are sniffed before anything is written to S3; the rest flows into an S3 multipart upload as it arrives.
func (s *Service) UploadAsset(stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]) error {
	ctx := stream.Context()
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	h, err := receiveHeader(stream)
	if err != nil {
		return err
	}
	up, err := s.beginUpload(ctx, ti, h)
	if err != nil {
		return err
	}

	reader := &chunkReader{stream: stream, max: s.pol.MaxSize(h.GetPurpose())}
	head, err := readHead(reader)
	if err != nil {
		return up.failedHead(stream, err)
	}
	if !policy.Sniff(up.contentType, head) {
		return up.rejected(stream, policy.ContentTypeMismatch)
	}

	sum := sha256.New()
	body := io.TeeReader(io.MultiReader(bytes.NewReader(head), reader), sum)
	size, done, err := up.store(ctx, stream, body)
	if err != nil || done {
		return err
	}
	out, err := up.complete(size, sum.Sum(nil))
	if err != nil {
		return internal(fmt.Errorf("complete proxied upload: %w", err))
	}
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: assetProto(out)})
}
