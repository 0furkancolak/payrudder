// Package command is the write side of the uploads module: opening an upload session, completing and aborting its
// files, and the proxied streaming upload. A file that becomes READY reserves its bytes through the quota module in the
// same transaction.
package command

import (
	"context"
	"log/slog"
	"time"

	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	quotacmd "github.com/0furkancolak/payrudder/services/assets-svc/internal/quota/command"
	quotaquery "github.com/0furkancolak/payrudder/services/assets-svc/internal/quota/query"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/uploads/query"
)

const (
	// maxFiles is the most files one session takes.
	maxFiles = 50
	// sha256Len is the length of a SHA-256 digest in bytes.
	sha256Len = 32
)

// Options configure Commands.
type Options struct {
	DB       store.DB
	Blob     store.Blob
	Policy   policy.Policy
	Sessions *query.Queries
	Usage    *quotaquery.Queries
	Quota    *quotacmd.Commands
	// SessionTTL is how long an upload session stays open.
	SessionTTL time.Duration
	Now        func() time.Time
	Log        *slog.Logger
}

// Commands are the state-changing operations of the uploads module.
type Commands struct {
	o   Options
	log *slog.Logger
}

// New returns the upload commands.
func New(o Options) *Commands { return &Commands{o: o, log: o.Log} }

// deleteObject removes an object whose asset was refused. A failure is logged: the object stays until the orphan
// reconciler removes it, which can take two days.
func (c *Commands) deleteObject(ctx context.Context, key string) {
	if err := c.o.Blob.Delete(ctx, key); err != nil {
		c.log.WarnContext(ctx, "assets: could not delete the object of a refused file", "object_key", key, "error", err)
	}
}
