package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/tenant"
)

func TestRoundTripThroughMetadata(t *testing.T) {
	id := uuid.New()
	ctx := tenant.WithOperator(tenant.NewContext(context.Background(),
		tenant.Info{ID: id, Environment: tenant.EnvSandbox, Timezone: "Europe/Istanbul", APIKeyID: "k1"}))

	out, _ := metadata.FromOutgoingContext(tenant.OutgoingContext(ctx))
	in := metadata.NewIncomingContext(context.Background(), out)

	var got tenant.Info
	var operator bool
	_, err := tenant.UnaryServerInterceptor()(in, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		got, _ = tenant.FromContext(ctx)
		operator = tenant.IsOperator(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := tenant.Info{ID: id, Environment: tenant.EnvSandbox, Timezone: "Europe/Istanbul", APIKeyID: "k1"}
	if got != want || !operator {
		t.Fatalf("got %+v operator=%v, want %+v", got, operator, want)
	}
}

func TestRequire(t *testing.T) {
	if _, err := tenant.Require(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Require without tenant: %v", err)
	}
	if err := tenant.RequireOperator(context.Background()); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("RequireOperator: %v", err)
	}
}

func TestMalformedTenantID(t *testing.T) {
	in := metadata.NewIncomingContext(context.Background(), metadata.Pairs(tenant.MDTenantID, "not-a-uuid"))
	_, err := tenant.UnaryServerInterceptor()(in, nil, &grpc.UnaryServerInfo{}, func(context.Context, any) (any, error) {
		t.Fatal("handler called")
		return nil, nil
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v", err)
	}
}
