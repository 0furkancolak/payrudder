//go:build integration

package itest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// testSandboxOnlyProductPath is phase 45 G on the path a person takes: a deployment configured with PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox (values-sandbox.yaml)
// cannot be made to hold a live tenant through the product API either. The app has only its sandbox, there is nothing to activate, and no live key can be
// asked for. (The operator path, CreateAccount, is covered by SandboxOnlyDeployment.)
func testSandboxOnlyProductPath(t *testing.T) {
	e := newEnv(t, "sandbox")
	ctx := context.Background()
	owner := e.verified(t, "sbx-only@example.com", "Sandbox Only Co")
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: owner.id, AccountId: owner.account, Name: "Only sandbox", Currency: "TRY", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	app := created.GetApp()
	if len(app.GetTenants()) != 1 || envOf(app, plansv1.Environment_ENVIRONMENT_SANDBOX) == nil || envOf(app, plansv1.Environment_ENVIRONMENT_LIVE) != nil {
		t.Fatalf("a sandbox-only deployment made the tenants %v", app.GetTenants())
	}

	// Nothing to activate.
	_, err = e.ident.GetMyGoLive(ctx, &plansv1.GetMyGoLiveRequest{UserId: owner.id, AppId: app.GetId()})
	if got := errkey.Of(err); status.Code(err) != codes.FailedPrecondition || got != "identity.go_live.no_live" {
		t.Errorf("go-live on a sandbox-only deployment: %v (key %q)", err, got)
	}
	_, err = e.ident.ActivateMyApp(ctx, &plansv1.ActivateMyAppRequest{UserId: owner.id, AppId: app.GetId(), AcceptTerms: true})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("activating a live environment that does not exist: %v", err)
	}

	// No live key, however it is asked for.
	_, err = e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_LIVE})
	if err == nil {
		t.Error("a live key was made on a sandbox-only deployment")
	}
	// The sandbox key is fine.
	if _, err := e.ident.CreateMyApiKey(ctx, &plansv1.CreateMyApiKeyRequest{UserId: owner.id, AppId: app.GetId(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX}); err != nil {
		t.Errorf("sandbox key: %v", err)
	}
}
