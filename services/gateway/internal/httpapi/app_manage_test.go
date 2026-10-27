package httpapi

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// What the rename and delete routes of an app passed on to plans-svc.
type appManageCalls struct {
	renames []*plansv1.RenameMyAppRequest
	deletes []*plansv1.DeleteMyAppRequest
}

func (f *fakeIdentity) RenameMyApp(_ context.Context, req *plansv1.RenameMyAppRequest) (*plansv1.RenameMyAppResponse, error) {
	f.seen(req.GetUserId())
	a, role := f.mine(req.GetUserId(), req.GetAppId())
	switch role {
	case "":
		return nil, notFound("app")
	case "viewer":
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "read-only")
	}
	f.mu.Lock()
	f.apps.renames = append(f.apps.renames, req)
	f.mu.Unlock()
	a.Name = req.GetName()
	return &plansv1.RenameMyAppResponse{App: a}, nil
}

func (f *fakeIdentity) DeleteMyApp(_ context.Context, req *plansv1.DeleteMyAppRequest) (*plansv1.DeleteMyAppResponse, error) {
	f.seen(req.GetUserId())
	_, role := f.mine(req.GetUserId(), req.GetAppId())
	switch role {
	case "":
		return nil, notFound("app")
	case "owner":
	default:
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "owners only")
	}
	f.mu.Lock()
	f.apps.deletes = append(f.apps.deletes, req)
	f.mu.Unlock()
	return &plansv1.DeleteMyAppResponse{ArchiveTime: timestamppb.Now()}, nil
}

// The rename and delete routes take the user from the session and the app from the path, and answer plans-svc's role rules as they are.
func TestAppRenameAndDeleteRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "PATCH", "/app/v1/apps/"+adaApp, tokAda, "", `{"name":"Ada Shop"}`)
	if resp.StatusCode != 200 || m["name"] != "Ada Shop" {
		t.Fatalf("rename: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.apps.renames[0]; got.GetUserId() != adaUser || got.GetAppId() != adaApp {
		t.Errorf("rename request = %v: the user comes from the session and the app from the path", got)
	}
	if resp, m := p.call(t, "PATCH", "/app/v1/apps/"+adaApp, tokViewer, "", `{"name":"x"}`); resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("a viewer renames: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "PATCH", "/app/v1/apps/"+bobApp, tokAda, "", `{"name":"x"}`); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "PATCH", "/app/v1/apps/"+adaApp, "", "", `{"name":"x"}`); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}

	if resp, m := p.call(t, "DELETE", "/app/v1/apps/"+adaApp, tokViewer, "", ""); resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("a viewer deletes: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+bobApp, tokAda, "", ""); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp, "", "", ""); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	if n := len(p.ident.apps.deletes); n != 0 {
		t.Fatalf("%d deletes got through before the owner asked", n)
	}
	if resp, _ := p.call(t, "DELETE", "/app/v1/apps/"+adaApp, tokAda, "", ""); resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if got := p.ident.apps.deletes[0]; got.GetUserId() != adaUser || got.GetAppId() != adaApp {
		t.Errorf("delete request = %v", got)
	}
}
