// Package grpcapi wires the modules into AssetsService: the uploads and assets handlers over the shared storage, and the
// quota module both of them use.
package grpcapi

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/assets"
	assetscmd "github.com/0furkancolak/payrudder/services/assets-svc/internal/assets/command"
	assetsquery "github.com/0furkancolak/payrudder/services/assets-svc/internal/assets/query"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/quota"
	quotacmd "github.com/0furkancolak/payrudder/services/assets-svc/internal/quota/command"
	quotaquery "github.com/0furkancolak/payrudder/services/assets-svc/internal/quota/query"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads"
	uploadscmd "github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads/command"
	uploadsquery "github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads/query"
)

// Blob is the object storage the service needs (*blob.Store).
type Blob = store.Blob

// Config tunes the service.
type Config struct {
	// Quotas are the storage limits in bytes by tenant tier ("free", "standard", "enterprise").
	Quotas map[string]int64
	// DefaultQuota applies to an unknown tier.
	DefaultQuota int64
	SessionTTL   time.Duration // default 24 h
	PresignTTL   time.Duration // default 15 min
	DownloadTTL  time.Duration // default 5 min
	Log          *slog.Logger
}

// Server implements assetsv1.AssetsServiceServer by delegating each method to the module that owns it.
type Server struct {
	*uploadHandlers
	*assetHandlers
	// The fallback sits one level deeper than the handlers, so a method a module serves always wins over it.
	fallback
}

// The embedded handlers are named by these aliases, so the two modules' Handler types do not clash as field names.
type (
	uploadHandlers = uploads.Handler
	assetHandlers  = assets.Handler
)

type fallback struct {
	assetsv1.UnimplementedAssetsServiceServer
}

// New returns the service over the app pool (assets_app, RLS enforced).
func New(pool *pgxpool.Pool, b Blob, pol policy.Policy, cfg Config) *Server {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.PresignTTL <= 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.DownloadTTL <= 0 {
		cfg.DownloadTTL = 5 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	db := store.NewDB(pool)
	limits := quota.NewLimits(cfg.Quotas, cfg.DefaultQuota)
	usage := quotaquery.New(db, limits)
	reserve := quotacmd.New(limits)

	sessions := uploadsquery.New(db, b, cfg.PresignTTL, time.Now)
	return &Server{
		uploadHandlers: uploads.New(uploadscmd.New(uploadscmd.Options{
			DB: db, Blob: b, Policy: pol, Sessions: sessions, Usage: usage, Quota: reserve,
			SessionTTL: cfg.SessionTTL, Now: time.Now, Log: cfg.Log,
		}), sessions),
		assetHandlers: assets.New(assetscmd.New(db, b, reserve), assetsquery.New(db, b, cfg.DownloadTTL, time.Now)),
	}
}
