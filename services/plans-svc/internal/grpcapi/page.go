package grpcapi

import (
	"encoding/base64"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Page tokens are opaque to clients: base64url of the last external_id returned.
func encodePageToken(lastExternalID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastExternalID))
}

func decodePageToken(tok string) (string, error) {
	if tok == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	return string(b), err
}

func convertTS(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
