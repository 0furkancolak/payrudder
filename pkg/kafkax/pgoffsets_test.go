package kafkax_test

import (
	"context"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/kafkax"
)

func TestPGOffsetsRejectsUnsafeTable(t *testing.T) {
	o := kafkax.PGOffsets{Table: "offsets; DROP TABLE x"}
	if _, err := o.Load(context.Background(), "t", []int32{0}); err == nil {
		t.Fatal("Load accepted an unsafe table name")
	}
	if err := o.Save(context.Background(), 0, 1); err == nil {
		t.Fatal("Save accepted an unsafe table name")
	}
}
