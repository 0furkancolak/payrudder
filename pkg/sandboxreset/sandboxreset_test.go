package sandboxreset_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/sandboxreset"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

func TestOnlyAnOperatorMayResetOnlyASandboxTenant(t *testing.T) {
	calls := 0
	srv := sandboxreset.New(func(_ context.Context, id uuid.UUID) (map[string]int64, error) {
		calls++
		return map[string]int64{"invoices": 3}, nil
	})
	op := tenant.WithOperator(context.Background())
	id := uuid.NewString()
	sandbox, live := plansv1.Environment_ENVIRONMENT_SANDBOX, plansv1.Environment_ENVIRONMENT_LIVE

	for name, tc := range map[string]struct {
		ctx  context.Context
		req  *adminv1.ResetTenantRequest
		code codes.Code
		key  string
	}{
		"not an operator":     {context.Background(), &adminv1.ResetTenantRequest{TenantId: id, Environment: sandbox}, codes.PermissionDenied, "identity.operator.forbidden"},
		"a live tenant":       {op, &adminv1.ResetTenantRequest{TenantId: id, Environment: live}, codes.FailedPrecondition, "admin.sandbox.live_refused"},
		"no environment":      {op, &adminv1.ResetTenantRequest{TenantId: id}, codes.FailedPrecondition, "admin.sandbox.live_refused"},
		"a tenant that isn't": {op, &adminv1.ResetTenantRequest{TenantId: "x", Environment: sandbox}, codes.InvalidArgument, "admin.sandbox.bad_tenant"},
		"the nil tenant":      {op, &adminv1.ResetTenantRequest{TenantId: uuid.Nil.String(), Environment: sandbox}, codes.InvalidArgument, "admin.sandbox.bad_tenant"},
	} {
		_, err := srv.ResetTenant(tc.ctx, tc.req)
		if status.Code(err) != tc.code || errkey.Of(err) != tc.key {
			t.Errorf("%s: %v (%q), want %v %q", name, err, errkey.Of(err), tc.code, tc.key)
		}
	}
	if calls != 0 {
		t.Fatalf("the purge ran %d times for refused requests", calls)
	}
	r, err := srv.ResetTenant(op, &adminv1.ResetTenantRequest{TenantId: id, Environment: sandbox})
	if err != nil || r.GetDeleted()["invoices"] != 3 || calls != 1 {
		t.Fatalf("reset = %v, %v (calls %d)", r, err, calls)
	}
}
