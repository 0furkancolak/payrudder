package api

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestBadQueryHidesTheBuilderText(t *testing.T) {
	err := badQuery(errors.New("query: cross-tenant queries are for operators"))
	if status.Code(err) != codes.InvalidArgument || errkey.Of(err) != "analytics.query.invalid" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(status.Convert(err).Message(), "query:") || strings.Contains(status.Convert(err).Message(), "operators") {
		t.Fatalf("the client saw the internal text: %q", status.Convert(err).Message())
	}
}
