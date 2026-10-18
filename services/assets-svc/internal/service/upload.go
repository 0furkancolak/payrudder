package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
)

// errTooLarge ends a stream that goes beyond its purpose's size limit.
var errTooLarge = errors.New("assets: file exceeds its size limit")

// chunkReader presents the chunks of an UploadAsset stream as an io.Reader. A client that goes away surfaces as the
// stream's error, which aborts the S3 multipart upload.
type chunkReader struct {
	stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]
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

// UploadAsset implements assetsv1.AssetsServiceServer: one file per stream, a header and then chunks. The first 512
// bytes are sniffed before anything is written to S3; the rest flows into an S3 multipart upload as it arrives.
func (s *Service) UploadAsset(stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]) error {
	ctx := stream.Context()
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return status.Error(codes.InvalidArgument, "the first message must be the upload header")
	}
	h := first.GetHeader()
	if h == nil {
		return status.Error(codes.InvalidArgument, "the first message must be the upload header")
	}
	name, err := policy.SanitizeFilename(h.GetFilename())
	if err != nil {
		return failure(codes.InvalidArgument, policy.InvalidFilename, "the filename is unusable")
	}
	ct, v := s.pol.Check(h.GetPurpose(), h.GetContentType(), max(h.GetSize(), 0))
	if v != nil {
		return fromViolation(v)
	}

	id := idgen.NewUUIDv7()
	key := objectKey(ti.ID, id)
	var declared *int64
	if h.GetSize() > 0 {
		sz := h.GetSize()
		declared = &sz
	}
	if declared != nil {
		var used int64
		if err := s.tx(ctx, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `SELECT bytes_used FROM storage_usage WHERE tenant_id = app_tenant_id()`).Scan(&used)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}); err != nil {
			return internal(err)
		}
		if used+*declared > s.quota(ti) {
			return failure(codes.FailedPrecondition, policy.StorageQuotaExceeded, "the file does not fit in the tenant's storage quota")
		}
	}
	if err := s.tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO assets (tenant_id, id, purpose, filename, content_type, declared_size, object_key, status)
			VALUES (app_tenant_id(), $1,$2,$3,$4,$5,$6,'UPLOADING')`, id, policy.PurposeName(h.GetPurpose()), name, ct, declared, key)
		return err
	}); err != nil {
		return internal(err)
	}

	// finish records how the upload ended; it must outlive a cancelled stream context.
	fctx := context.WithoutCancel(ctx)
	end := func(statusName, reason string) (*asset, error) {
		var a *asset
		err := s.tx(fctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(fctx, `UPDATE assets SET status = $2, reject_reason = NULLIF($3, '') WHERE id = $1`, id, statusName, reason); err != nil {
				return err
			}
			var err error
			a, err = loadAsset(fctx, tx, id, false)
			return err
		})
		return a, err
	}

	limit := s.pol.MaxSize(h.GetPurpose())
	reader := &chunkReader{stream: stream, max: limit}
	// Sniff the head before writing anything.
	head := make([]byte, 0, 512)
	for len(head) < 512 {
		buf := make([]byte, 512-len(head))
		n, rerr := reader.Read(buf)
		head = append(head, buf[:n]...)
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			a, _ := end("REJECTED", policy.FileTooLarge)
			if errors.Is(rerr, errTooLarge) && a != nil {
				return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: a.proto()})
			}
			_, _ = end("ABORTED", "")
			return status.FromContextError(rerr).Err()
		}
	}
	if !policy.Sniff(ct, head) {
		a, err := end("REJECTED", policy.ContentTypeMismatch)
		if err != nil {
			return internal(err)
		}
		return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: a.proto()})
	}

	sum := sha256.New()
	body := io.TeeReader(io.MultiReader(bytes.NewReader(head), reader), sum)
	err = s.blob.PutStream(ctx, key, body, blob.StreamOptions{
		PutOptions: blob.PutOptions{ContentType: ct, Tags: map[string]string{"purpose": policy.PurposeName(h.GetPurpose())}},
	})
	if err != nil {
		// The upload manager aborted the multipart upload: no parts are left behind.
		if errors.Is(err, errTooLarge) {
			a, _ := end("REJECTED", policy.FileTooLarge)
			if a != nil {
				return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: a.proto()})
			}
		}
		_, _ = end("ABORTED", "")
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}
		return status.Error(codes.Unavailable, "object storage unavailable")
	}
	info, err := s.blob.Head(fctx, key)
	if err != nil {
		_, _ = end("ABORTED", "")
		return status.Error(codes.Unavailable, "object storage unavailable")
	}
	var out *asset
	err = s.tx(fctx, func(tx pgx.Tx) error {
		a, err := loadAsset(fctx, tx, id, true)
		if err != nil {
			return err
		}
		out, err = s.finish(fctx, tx, ti, a, info.Size, sum.Sum(nil))
		return err
	})
	if err != nil {
		return internal(fmt.Errorf("complete proxied upload: %w", err))
	}
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: out.proto()})
}
