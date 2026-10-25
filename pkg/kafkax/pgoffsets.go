package kafkax

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// offsetSQL holds the fixed statements of each offsets table (ADR 0020 section 5: SQL is never assembled at run time).
type offsetSQL struct{ load, save string }

var offsetTables = map[string]offsetSQL{
	"consumer_offsets": {
		load: `SELECT partition, next_offset FROM consumer_offsets WHERE consumer_group = $1 AND topic = $2 AND partition = ANY($3::int[])`,
		save: `INSERT INTO consumer_offsets (consumer_group, topic, partition, next_offset) VALUES ($1, $2, $3, $4)
ON CONFLICT (consumer_group, topic, partition)
DO UPDATE SET next_offset = GREATEST(consumer_offsets.next_offset, EXCLUDED.next_offset), updated_at = now()`,
	},
	"kafka_offsets": {
		load: `SELECT partition, next_offset FROM kafka_offsets WHERE consumer_group = $1 AND topic = $2 AND partition = ANY($3::int[])`,
		save: `INSERT INTO kafka_offsets (consumer_group, topic, partition, next_offset) VALUES ($1, $2, $3, $4)
ON CONFLICT (consumer_group, topic, partition)
DO UPDATE SET next_offset = GREATEST(kafka_offsets.next_offset, EXCLUDED.next_offset), updated_at = now()`,
	},
}

func (o PGOffsets) sql() (offsetSQL, error) {
	q, ok := offsetTables[o.Table]
	if !ok {
		return offsetSQL{}, fmt.Errorf("kafkax: invalid offsets table %q", o.Table)
	}
	return q, nil
}

// PGDB is the part of a pgx pool (or transaction) PGOffsets needs.
type PGDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// PGOffsets keeps the next offset of each partition of Topic in a Postgres table with the columns consumer_group, topic,
// partition, next_offset and updated_at, keyed by the first three (aggregator's consumer_offsets, notifier's
// kafka_offsets; the names predate this type and stay). Table must be one of those two names; anything else fails the
// call.
type PGOffsets struct {
	DB    PGDB
	Group string
	Topic string // used by Save; Load takes the topic from the consumer
	Table string
}

// Load implements OffsetLoader. A partition without a row is absent from the result.
func (o PGOffsets) Load(ctx context.Context, topic string, partitions []int32) (map[int32]int64, error) {
	q, err := o.sql()
	if err != nil {
		return nil, err
	}
	rows, err := o.DB.Query(ctx, q.load, o.Group, topic, partitions)
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
	q, err := o.sql()
	if err != nil {
		return err
	}
	_, err = o.DB.Exec(ctx, q.save, o.Group, o.Topic, partition, next)
	return err
}
