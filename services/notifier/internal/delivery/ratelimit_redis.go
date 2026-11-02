package delivery

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

//go:embed ratelimit.lua
var bucketLua string

var bucketScript = redis.NewScript(bucketLua)

// Shared keeps the tenants' buckets outside the process, so that every replica draws from the same one (Limiter.Shared). Take answers whether the tenant had a token;
// an error means the buckets could not be reached, and the Limiter then decides on its own copy.
type Shared interface {
	Take(ctx context.Context, tenantID uuid.UUID, perMinute, burst int) (bool, error)
}

// RedisBuckets is Shared on Redis: one key per tenant, a generic cell rate step in a script (the gateway's rate limiter uses the same one for its requests), the
// time taken from Redis so that replicas with different clocks agree.
type RedisBuckets struct {
	Client redis.Scripter
	// Prefix starts every key ("nf:mail:rl:" when empty).
	Prefix string
}

// Take implements Shared.
func (r RedisBuckets) Take(ctx context.Context, tenantID uuid.UUID, perMinute, burst int) (bool, error) {
	if r.Client == nil {
		return false, fmt.Errorf("no redis")
	}
	prefix := r.Prefix
	if prefix == "" {
		prefix = "nf:mail:rl:"
	}
	emission := (time.Minute / time.Duration(perMinute)).Microseconds()
	res, err := bucketScript.Run(ctx, r.Client, []string{prefix + tenantID.String()}, emission, max(burst, 1), 1, 0).Int64Slice()
	if err != nil {
		return false, err
	}
	if len(res) != 2 {
		return false, fmt.Errorf("mail rate limit: unexpected reply %v", res)
	}
	return res[0] == 1, nil
}
