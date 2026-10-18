package runs

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampOf(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
