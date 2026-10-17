package admin

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func convertTS(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
