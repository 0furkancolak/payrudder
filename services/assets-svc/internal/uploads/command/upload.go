package command

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// errTooLarge ends a stream that goes beyond its purpose's size limit.
var errTooLarge = errors.New("assets: file exceeds its size limit")

// Stream is the client stream of UploadAsset (a grpc.ClientStreamingServer).
type Stream interface {
	Context() context.Context
	Recv() (*assetsv1.UploadAssetRequest, error)
	SendAndClose(*assetsv1.UploadAssetResponse) error
}

// chunkReader presents the chunks of an UploadAsset stream as an io.Reader. A client that goes away surfaces as the
// stream's error, which aborts the S3 multipart upload.
type chunkReader struct {
	stream Stream
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

// rejection is the error of an upload that was ended as REJECTED; the stream is answered with the rejected asset.
type rejection struct{ asset sqlc.Asset }

func (*rejection) Error() string { return "assets: upload rejected" }

// proxiedUpload is one UploadAsset stream whose asset row exists.
type proxiedUpload struct {
	c           *Commands
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
	err := u.c.o.DB.Tx(u.ctx, func(q *sqlc.Queries) error {
		if err := q.SetAssetOutcome(u.ctx, sqlc.SetAssetOutcomeParams{ID: u.id, Status: statusName, Reason: reason}); err != nil {
			return err
		}
		var err error
		a, err = store.GetAsset(u.ctx, q, u.id)
		return err
	})
	return a, err
}

// abort ends the upload as ABORTED. A failure is logged: the asset then stays UPLOADING.
func (u *proxiedUpload) abort() {
	if _, err := u.end(store.AssetAborted, ""); err != nil {
		u.c.log.ErrorContext(u.ctx, "assets: could not mark an interrupted upload ABORTED", "asset_id", u.id, "error", err)
	}
}

// reject ends the upload as REJECTED.
func (u *proxiedUpload) reject(reason string) (*rejection, error) {
	a, err := u.end(store.AssetRejected, reason)
	if err != nil {
		return nil, err
	}
	return &rejection{asset: a}, nil
}

// complete turns the stored object into a READY asset.
func (u *proxiedUpload) complete(size int64, sha []byte) (sqlc.Asset, error) {
	var out sqlc.Asset
	err := u.c.o.DB.Tx(u.ctx, func(q *sqlc.Queries) error {
		a, err := store.LockAsset(u.ctx, q, u.id)
		if err != nil {
			return err
		}
		out, err = u.c.finish(u.ctx, q, u.ti, a, size, sha)
		return err
	})
	return out, err
}

// receiveHeader reads the first message of the stream, which must be the upload header.
func receiveHeader(stream Stream) (*assetsv1.UploadHeader, error) {
	first, err := stream.Recv()
	if err != nil || first.GetHeader() == nil {
		return nil, errkey.New(codes.InvalidArgument, "assets.upload.header_required", "the first message must be the upload header")
	}
	return first.GetHeader(), nil
}

// beginUpload validates the header, checks the quota for a declared size and records the UPLOADING asset.
func (c *Commands) beginUpload(ctx context.Context, ti tenant.Info, h *assetsv1.UploadHeader) (*proxiedUpload, error) {
	name, err := policy.SanitizeFilename(h.GetFilename())
	if err != nil {
		return nil, apierr.Failure(codes.InvalidArgument, policy.InvalidFilename, "the filename is unusable")
	}
	ct, v := c.o.Policy.Check(h.GetPurpose(), h.GetContentType(), max(h.GetSize(), 0))
	if v != nil {
		return nil, apierr.FromViolation(v)
	}
	id := idgen.NewUUIDv7()
	key := store.ObjectKey(ti.ID, id)
	var declared *int64
	if h.GetSize() > 0 {
		sz := h.GetSize()
		declared = &sz
		used, err := c.o.Usage.Used(ctx)
		if err != nil {
			return nil, apierr.Internal(ctx, err)
		}
		if used+sz > c.o.Usage.Limit(ti) {
			return nil, apierr.Failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the file does not fit in the tenant's storage quota")
		}
	}
	err = c.o.DB.Tx(ctx, func(q *sqlc.Queries) error {
		return q.InsertProxiedAsset(ctx, sqlc.InsertProxiedAssetParams{
			ID: id, Purpose: policy.PurposeName(h.GetPurpose()), Filename: name, ContentType: ct, DeclaredSize: declared, ObjectKey: key,
		})
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return &proxiedUpload{c: c, ti: ti, id: id, key: key, contentType: ct, purpose: h.GetPurpose(), ctx: context.WithoutCancel(ctx)}, nil
}

// readHead reads up to policy.SniffBytes bytes of the stream for content sniffing.
func readHead(r io.Reader) ([]byte, error) {
	head := make([]byte, 0, policy.SniffBytes)
	for len(head) < policy.SniffBytes {
		buf := make([]byte, policy.SniffBytes-len(head))
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
func (u *proxiedUpload) failedHead(rerr error) error {
	if errors.Is(rerr, errTooLarge) {
		rej, err := u.reject(policy.FileTooLarge)
		if err == nil {
			return rej
		}
		u.c.log.ErrorContext(u.ctx, "assets: could not record a rejected upload", "asset_id", u.id, "error", err)
	}
	u.abort()
	return status.FromContextError(rerr).Err()
}

// put streams the body into S3 and returns the object's size. A stream that went past its limit ends as REJECTED
// and is reported as a *rejection.
func (u *proxiedUpload) put(ctx context.Context, body io.Reader) (int64, error) {
	err := u.c.o.Blob.PutStream(ctx, u.key, body, blob.StreamOptions{
		PutOptions: blob.PutOptions{ContentType: u.contentType, Tags: map[string]string{"purpose": policy.PurposeName(u.purpose)}},
	})
	if err != nil {
		// The upload manager aborted the multipart upload: no parts are left behind.
		if errors.Is(err, errTooLarge) {
			if rej, rerr := u.reject(policy.FileTooLarge); rerr == nil {
				return 0, rej
			}
		}
		u.abort()
		if ctx.Err() != nil {
			return 0, status.FromContextError(ctx.Err()).Err()
		}
		return 0, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	info, err := u.c.o.Blob.Head(u.ctx, u.key)
	if err != nil {
		u.abort()
		return 0, errkey.New(codes.Unavailable, "assets.storage.unavailable", "object storage unavailable")
	}
	return info.Size, nil
}

// answer sends the asset to the client and closes the stream.
func answer(stream Stream, a sqlc.Asset) error {
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: convert.Asset(a)})
}

// Upload is the UploadAsset call: one file per stream, a header and then chunks. The first policy.SniffBytes bytes are
// sniffed before anything is written to S3; the rest flows into an S3 multipart upload as it arrives. The result is
// sent on the stream.
func (c *Commands) Upload(stream Stream) error {
	ctx := stream.Context()
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	h, err := receiveHeader(stream)
	if err != nil {
		return err
	}
	up, err := c.beginUpload(ctx, ti, h)
	if err != nil {
		return err
	}

	reader := &chunkReader{stream: stream, max: c.o.Policy.MaxSize(h.GetPurpose())}
	head, err := readHead(reader)
	if err != nil {
		return answerRejection(stream, up.failedHead(err))
	}
	if !policy.Sniff(up.contentType, head) {
		rej, err := up.reject(policy.ContentTypeMismatch)
		if err != nil {
			return apierr.Internal(ctx, err)
		}
		return answer(stream, rej.asset)
	}

	sum := sha256.New()
	body := io.TeeReader(io.MultiReader(bytes.NewReader(head), reader), sum)
	size, err := up.put(ctx, body)
	if err != nil {
		return answerRejection(stream, err)
	}
	out, err := up.complete(size, sum.Sum(nil))
	if err != nil {
		return apierr.Internal(ctx, fmt.Errorf("complete proxied upload: %w", err))
	}
	return answer(stream, out)
}

// answerRejection answers the stream with the rejected asset when err is a *rejection and passes any other error on.
func answerRejection(stream Stream, err error) error {
	var rej *rejection
	if errors.As(err, &rej) {
		return answer(stream, rej.asset)
	}
	return err
}
