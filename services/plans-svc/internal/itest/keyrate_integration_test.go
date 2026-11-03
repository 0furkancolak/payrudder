//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A key's own rate limit (plan 20): stored with the key, resolved for the gateway, kept by a rotation, refused outside 1 to 100 (by the code and by the column).
func testAPIKeyRatePercent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.verified(t, "keyrate-owner@example.com", "Keyrate Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Shop", Currency: "TRY", Timezone: "Europe/Istanbul"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	sandbox := plansv1.Environment_ENVIRONMENT_SANDBOX

	k, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: sandbox, RatePercent: 30})
	if err != nil {
		t.Fatal(err)
	}
	if k.GetApiKey().GetRatePercent() != 30 {
		t.Errorf("created key rate_percent = %d, want 30", k.GetApiKey().GetRatePercent())
	}
	r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: k.GetApiKey().GetPrefix()})
	if err != nil || r.GetRatePercent() != 30 {
		t.Fatalf("resolve: %v, %v; want rate_percent 30", r, err)
	}

	rot, err := e.ident.RotateMyApiKey(ctx, &plansv1.RotateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), ApiKeyId: k.GetApiKey().GetId()})
	if err != nil || rot.GetApiKey().GetRatePercent() != 30 {
		t.Fatalf("rotate: %v, %v; the replacement keeps the limit", rot, err)
	}

	plain, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: sandbox})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := e.admin.ResolveApiKey(ctx, &plansv1.ResolveApiKeyRequest{Prefix: plain.GetApiKey().GetPrefix()}); err != nil || r.GetRatePercent() != 0 {
		t.Errorf("a key without a percentage: %v, %v; want 0", r, err)
	}

	for _, bad := range []int32{-1, 101} {
		_, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: sandbox, RatePercent: bad})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("rate_percent %d: %v, want InvalidArgument", bad, err)
		}
	}
	if _, err := e.system.Exec(ctx, `UPDATE api_keys SET rate_percent = 0 WHERE id = $1::uuid`, k.GetApiKey().GetId()); err == nil {
		t.Error("the column accepted 0 (NULL means no limit)")
	}
	if _, err := e.system.Exec(ctx, `UPDATE api_keys SET rate_percent = 101 WHERE id = $1::uuid`, k.GetApiKey().GetId()); err == nil {
		t.Error("the column accepted 101")
	}
}
