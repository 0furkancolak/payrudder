// Package query is the read side of the assets module.
package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

const (
	// DefaultPageSize and MaxPageSize bound a ListAssets page.
	DefaultPageSize = 100
	MaxPageSize     = 500
)

// Queries read assets.
type Queries struct {
	db          store.DB
	blob        store.Blob
	downloadTTL time.Duration
	now         func() time.Time
}

// New returns the asset queries; downloadTTL is how long a download URL stays valid.
func New(db store.DB, b store.Blob, downloadTTL time.Duration, now func() time.Time) *Queries {
	return &Queries{db: db, blob: b, downloadTTL: downloadTTL, now: now}
}

func (q *Queries) read(ctx context.Context, id uuid.UUID) (sqlc.Asset, error) {
	var a sqlc.Asset
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		var err error
		a, err = store.GetAsset(ctx, tx, id)
		return err
	})
	if err != nil {
		return a, apierr.Internal(ctx, err)
	}
	return a, nil
}

// Get returns one asset.
func (q *Queries) Get(ctx context.Context, id uuid.UUID) (*assetsv1.Asset, error) {
	a, err := q.read(ctx, id)
	if err != nil {
		return nil, err
	}
	return convert.Asset(a), nil
}

// List returns a page of READY assets of a purpose (all purposes when unspecified), newest first, with the token of the next page.
func (q *Queries) List(ctx context.Context, req *assetsv1.ListAssetsRequest) (*assetsv1.ListAssetsResponse, error) {
	size32, ok := page.Size(req.GetPageSize(), DefaultPageSize, MaxPageSize)
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "assets.list.bad_page_size", "page_size must be 1..500")
	}
	size := int(size32)
	after := uuid.Nil
	if t := req.GetPageToken(); t != "" {
		var err error
		if after, err = uuid.Parse(t); err != nil {
			return nil, errkey.New(codes.InvalidArgument, "assets.list.bad_page_token", "page_token is invalid")
		}
	}
	var rows []sqlc.Asset
	err := q.db.Tx(ctx, func(tx *sqlc.Queries) error {
		var err error
		rows, err = tx.ListReadyAssets(ctx, sqlc.ListReadyAssetsParams{Purpose: policy.PurposeName(req.GetPurpose()), After: after, PageSize: size32 + 1})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	resp := &assetsv1.ListAssetsResponse{}
	for _, a := range rows {
		if len(resp.Assets) == size {
			resp.NextPageToken = resp.Assets[size-1].Id
			break
		}
		resp.Assets = append(resp.Assets, convert.Asset(a))
	}
	return resp, nil
}

// DownloadURL signs a time-limited download URL of a READY asset.
func (q *Queries) DownloadURL(ctx context.Context, id uuid.UUID) (url string, expires time.Time, err error) {
	a, err := q.read(ctx, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if a.Status != store.AssetReady {
		return "", time.Time{}, errkey.Newf(codes.FailedPrecondition, "assets.download.not_ready", "the asset is %s, only READY assets can be downloaded", a.Status)
	}
	u, err := q.blob.PresignDownload(ctx, a.ObjectKey, a.Filename, q.downloadTTL)
	if err != nil {
		return "", time.Time{}, errkey.New(codes.Unavailable, "assets.download.sign_failed", "could not sign the download link")
	}
	return u, q.now().Add(q.downloadTTL), nil
}
