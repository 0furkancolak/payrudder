package runs

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampOf(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func i32(n int) int32 { return int32(n) } //nolint:gosec // attempt counters, page sizes and calendar years are small

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
