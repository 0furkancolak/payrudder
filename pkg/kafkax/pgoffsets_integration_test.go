//go:build integration

package kafkax_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/testkit"
)

func TestPGOffsetsLoadSaveNeverBackwards(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "offt")
	conn, err := pgx.Connect(ctx, db.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE kafka_offsets (
  consumer_group text NOT NULL, topic text NOT NULL, partition int NOT NULL, next_offset bigint NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (consumer_group, topic, partition))`); err != nil {
		t.Fatal(err)
	}
	o := kafkax.PGOffsets{DB: conn, Group: "g", Topic: "t", Table: "kafka_offsets"}
	for _, next := range []int64{5, 9, 7} {
		if err := o.Save(ctx, 1, next); err != nil {
			t.Fatal(err)
		}
	}
	got, err := o.Load(ctx, "t", []int32{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[1] != 9 {
		t.Fatalf("offsets = %v, want map[1:9]", got)
	}
}
