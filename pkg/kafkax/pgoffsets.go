package kafkax

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var tableName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// PGDB is the part of a pgx pool (or transaction) PGOffsets needs.
type PGDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PGOffsets keeps the next offset of each partition of Topic in a Postgres table with the columns consumer_group, topic,
// partition, next_offset and updated_at, keyed by the first three (aggregator's consumer_offsets, notifier's
// kafka_offsets; the names predate this type and stay). Table is
// interpolated into SQL, so it must be a plain lower-case identifier (a constant of the service); anything else fails
// the call.
type PGOffsets struct {
	DB    PGDB
	Group string
	Topic string // used by Save; Load takes the topic from the consumer
	Table string
}

// Load implements OffsetLoader. A partition without a row is absent from the result.
func (o PGOffsets) Load(ctx context.Context, topic string, partitions []int32) (map[int32]int64, error) {
	if !tableName.MatchString(o.Table) {
		return nil, fmt.Errorf("kafkax: invalid offsets table %q", o.Table)
	}
	rows, err := o.DB.Query(ctx,
		`SELECT partition, next_offset FROM `+o.Table+` WHERE consumer_group = $1 AND topic = $2 AND partition = ANY($3::int[])`,
		o.Group, topic, partitions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int32]int64, len(partitions))
	for rows.Next() {
		var p int32
		var next int64
		if err := rows.Scan(&p, &next); err != nil {
			return nil, err
		}
		out[p] = next
	}
	return out, rows.Err()
}

// Save advances the offset of a partition, never backwards.
func (o PGOffsets) Save(ctx context.Context, partition int32, next int64) error {
	if !tableName.MatchString(o.Table) {
		return fmt.Errorf("kafkax: invalid offsets table %q", o.Table)
	}
	_, err := o.DB.Exec(ctx, `INSERT INTO `+o.Table+` (consumer_group, topic, partition, next_offset) VALUES ($1, $2, $3, $4)
ON CONFLICT (consumer_group, topic, partition)
DO UPDATE SET next_offset = GREATEST(`+o.Table+`.next_offset, EXCLUDED.next_offset), updated_at = now()`,
		o.Group, o.Topic, partition, next)
	return err
}
