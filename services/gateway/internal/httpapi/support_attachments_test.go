package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// helpDesk is a plans-svc help desk and an assets-svc that record what the gateway asks of them, in order, with the tenant each assets call was made for.
type helpDesk struct {
	plansv1.UnimplementedSupportServiceServer
	assetsv1.UnimplementedAssetsServiceServer
	mu       sync.Mutex
	calls    []string
	addErr   error
	rejected string // assets-svc refuses the file with this reason
	authReq  *plansv1.AuthorizeSupportAttachmentRequest
	addReq   *plansv1.AddSupportAttachmentRequest
	getReq   *plansv1.GetSupportAttachmentRequest
	assetsAs []string // "<call> <tenant>"
	purpose  assetsv1.Purpose
}

const helpDeskTenant = "33333333-3333-4333-8333-333333333333"

func (h *helpDesk) note(s string) { h.mu.Lock(); h.calls = append(h.calls, s); h.mu.Unlock() }

func (h *helpDesk) AuthorizeSupportAttachment(_ context.Context, req *plansv1.AuthorizeSupportAttachmentRequest) (*plansv1.AuthorizeSupportAttachmentResponse, error) {
	h.note("authorize")
	h.authReq = req
	return &plansv1.AuthorizeSupportAttachmentResponse{Target: &plansv1.SupportAttachmentTarget{TenantId: helpDeskTenant, Environment: "live", Timezone: "Europe/Berlin", Tier: "standard"}}, nil
}

func (h *helpDesk) AddSupportAttachment(_ context.Context, req *plansv1.AddSupportAttachmentRequest) (*plansv1.AddSupportAttachmentResponse, error) {
	h.note("add")
	h.addReq = req
	if h.addErr != nil {
		return nil, h.addErr
	}
	return &plansv1.AddSupportAttachmentResponse{Attachment: &plansv1.SupportAttachment{Id: "att-1", Filename: req.GetFilename(), ContentType: req.GetContentType(), SizeBytes: req.GetSizeBytes(), CreateTime: timestamppb.Now()}}, nil
}

func (h *helpDesk) GetSupportAttachment(_ context.Context, req *plansv1.GetSupportAttachmentRequest) (*plansv1.GetSupportAttachmentResponse, error) {
	h.note("get")
	h.getReq = req
	return &plansv1.GetSupportAttachmentResponse{Target: &plansv1.SupportAttachmentTarget{TenantId: helpDeskTenant, Environment: "live", Timezone: "UTC", Tier: "free", AssetId: "asset-9", Filename: "shot.png"}}, nil
}

func (h *helpDesk) UploadAsset(stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	ti, _ := tenant.FromContext(stream.Context())
	h.mu.Lock()
	h.calls = append(h.calls, "upload")
	h.assetsAs = append(h.assetsAs, "upload "+ti.ID.String())
	h.purpose = first.GetHeader().GetPurpose()
	h.mu.Unlock()
	var n int64
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		n += int64(len(m.GetChunk()))
	}
	if h.rejected != "" {
		return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: &assetsv1.Asset{Id: "rejected", Status: assetsv1.AssetStatus_ASSET_STATUS_REJECTED, RejectReason: h.rejected}})
	}
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: &assetsv1.Asset{Id: "asset-1", Filename: first.GetHeader().GetFilename(), ContentType: first.GetHeader().GetContentType(),
		Purpose: first.GetHeader().GetPurpose(), SizeBytes: n, Status: assetsv1.AssetStatus_ASSET_STATUS_READY}})
}

func (h *helpDesk) DeleteAsset(ctx context.Context, req *assetsv1.DeleteAssetRequest) (*assetsv1.DeleteAssetResponse, error) {
	ti, _ := tenant.FromContext(ctx)
	h.mu.Lock()
	h.calls = append(h.calls, "delete")
	h.assetsAs = append(h.assetsAs, "delete "+req.GetAssetId()+" "+ti.ID.String())
	h.mu.Unlock()
	return &assetsv1.DeleteAssetResponse{}, nil
}

func (h *helpDesk) GetDownloadUrl(ctx context.Context, req *assetsv1.GetDownloadUrlRequest) (*assetsv1.GetDownloadUrlResponse, error) {
	ti, _ := tenant.FromContext(ctx)
	h.mu.Lock()
	h.calls = append(h.calls, "link")
	h.assetsAs = append(h.assetsAs, "link "+req.GetAssetId()+" "+ti.ID.String())
	h.mu.Unlock()
	return &assetsv1.GetDownloadUrlResponse{Url: "http://s3/support?sig=1", ExpireTime: timestamppb.Now()}, nil
}

func newHelpDesk(t *testing.T) (*product, *helpDesk) {
	t.Helper()
	h := &helpDesk{}
	gs, _ := grpcx.NewServer(grpcx.ServerOptions{Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor()), grpc.ChainStreamInterceptor(tenant.StreamServerInterceptor())}})
	plansv1.RegisterSupportServiceServer(gs, h)
	assetsv1.RegisterAssetsServiceServer(gs, h)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithChainUnaryInterceptor(tenant.UnaryClientInterceptor()), grpc.WithChainStreamInterceptor(tenant.StreamClientInterceptor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	p := newProduct(t, nil, func(d *Deps) {
		d.Support = plansv1.NewSupportServiceClient(cc)
		d.Assets = assetsv1.NewAssetsServiceClient(cc)
	})
	return p, h
}

func attachBody(t *testing.T, name, contentType string, size int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hd := textproto.MIMEHeader{}
	hd.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	hd.Set("Content-Type", contentType)
	fw, _ := mw.CreatePart(hd)
	_, _ = fw.Write(bytes.Repeat([]byte{'x'}, size))
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

func (p *product) upload(t *testing.T, path, token string, body *bytes.Buffer, ctype string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.ts.URL+path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

const supportMessagePath = "/app/v1/support/tickets/t-1/messages/m-1/attachments"

// A person's file is checked by the help desk, stored by assets-svc under the tenant the help desk named with the purpose support_attachment, and then recorded;
// the person is the session's, whatever the form says.
func TestAPersonAttachesAFileUnderTheTenantTheHelpDeskNames(t *testing.T) {
	p, h := newHelpDesk(t)
	body, ctype := attachBody(t, "shot.png", "image/png", 2048)
	resp, out := p.upload(t, supportMessagePath, tokAda, body, ctype)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(out, `"att-1"`) {
		t.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	if got := strings.Join(h.calls, ","); got != "authorize,upload,add" {
		t.Fatalf("calls: %s", got)
	}
	if h.authReq.GetUserId() != adaUser || h.authReq.GetTicketId() != "t-1" || h.authReq.GetMessageId() != "m-1" {
		t.Errorf("authorize: %+v", h.authReq)
	}
	if h.purpose != assetsv1.Purpose_PURPOSE_SUPPORT_ATTACHMENT || h.assetsAs[0] != "upload "+helpDeskTenant {
		t.Errorf("the file was stored as purpose %v, %v", h.purpose, h.assetsAs)
	}
	if a := h.addReq; a.GetUserId() != adaUser || a.GetTenantId() != helpDeskTenant || a.GetAssetId() != "asset-1" || a.GetSizeBytes() != 2048 || a.GetContentType() != "image/png" || a.GetFilename() != "shot.png" {
		t.Errorf("add: %+v", a)
	}
	if resp, _ := p.upload(t, supportMessagePath, "", bytes.NewBufferString("x"), "multipart/form-data; boundary=x"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no session: %d", resp.StatusCode)
	}
}

// A file the help desk refuses to record is taken out of the storage again; one assets-svc refuses is never recorded and is answered with the help desk's own key.
func TestAnAttachmentThatIsNotRecordedIsTakenBackOutOfTheStorage(t *testing.T) {
	p, h := newHelpDesk(t)
	h.addErr = errkey.New(codes.ResourceExhausted, "support.attachment.too_many", "a ticket can hold at most 10 files")
	body, ctype := attachBody(t, "shot.png", "image/png", 100)
	resp, out := p.upload(t, supportMessagePath, tokAda, body, ctype)
	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(out, "support.attachment.too_many") {
		t.Fatalf("refused add: %d %s", resp.StatusCode, out)
	}
	if got := strings.Join(h.calls, ","); got != "authorize,upload,add,delete" || h.assetsAs[1] != "delete asset-1 "+helpDeskTenant {
		t.Fatalf("calls: %s %v", got, h.assetsAs)
	}

	p, h = newHelpDesk(t)
	h.rejected = "FILE_TOO_LARGE"
	body, ctype = attachBody(t, "big.png", "image/png", 100)
	resp, out = p.upload(t, supportMessagePath, tokAda, body, ctype)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, "support.attachment.too_large") {
		t.Fatalf("refused file: %d %s", resp.StatusCode, out)
	}
	if got := strings.Join(h.calls, ","); got != "authorize,upload" {
		t.Fatalf("a refused file was recorded or removed: %s", got)
	}

	p, h = newHelpDesk(t)
	h.rejected = "CONTENT_TYPE_MISMATCH"
	body, ctype = attachBody(t, "fake.png", "image/png", 100)
	if resp, out = p.upload(t, supportMessagePath, tokAda, body, ctype); resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, "support.attachment.bad_type") {
		t.Fatalf("spoofed file: %d %s", resp.StatusCode, out)
	}
}

// A download asks the help desk where the file is and then assets-svc, for that tenant, for a link; the operator's route names no person.
func TestAnAttachmentIsFetchedFromTheTenantItIsStoredUnder(t *testing.T) {
	p, h := newHelpDesk(t)
	resp, m := p.call(t, "GET", "/app/v1/support/tickets/t-1/attachments/a-1", tokAda, "", "")
	if resp.StatusCode != http.StatusOK || m["url"] != "http://s3/support?sig=1" {
		t.Fatalf("person's link: %d %v", resp.StatusCode, m)
	}
	if h.getReq.GetUserId() != adaUser || h.getReq.GetTicketId() != "t-1" || h.getReq.GetAttachmentId() != "a-1" || h.assetsAs[0] != "link asset-9 "+helpDeskTenant {
		t.Errorf("person: %+v %v", h.getReq, h.assetsAs)
	}
	resp, m = p.call(t, "GET", "/admin/v1/support/tickets/t-1/attachments/a-1", operatorToken, "", "")
	if resp.StatusCode != http.StatusOK || m["url"] == nil || h.getReq.GetUserId() != "" {
		t.Fatalf("operator's link: %d %v %+v", resp.StatusCode, m, h.getReq)
	}
	if resp, _ := p.call(t, "GET", "/admin/v1/support/tickets/t-1/attachments/a-1", tokAda, "", ""); resp.StatusCode < 400 {
		t.Errorf("a person reached the operator's route: %d", resp.StatusCode)
	}
	body, ctype := attachBody(t, "trace.txt", "text/plain", 10)
	resp, out := p.upload(t, "/admin/v1/support/tickets/t-1/messages/m-1/attachments", operatorToken, body, ctype)
	if resp.StatusCode != http.StatusCreated || h.authReq.GetUserId() != "" {
		t.Fatalf("operator's upload: %d %s %+v", resp.StatusCode, out, h.authReq)
	}
}

// The operators' addresses are the list a ticket can be assigned to.
func TestTheAssigneesAreTheOperatorsOfTheDeployment(t *testing.T) {
	p := newProduct(t, nil, func(d *Deps) {
		d.OperatorEmails = NormalizeOperatorEmails([]string{"Boss@example.com", "sam@example.com"})
	})
	resp, m := p.call(t, "GET", "/admin/v1/support/operators", operatorToken, "", "")
	ops, _ := m["operators"].([]any)
	if resp.StatusCode != http.StatusOK || len(ops) != 2 || ops[0] != "boss@example.com" {
		t.Fatalf("operators: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "GET", "/admin/v1/support/operators", tokAda, "", ""); resp.StatusCode < 400 {
		t.Errorf("a person read the operators' list: %d", resp.StatusCode)
	}
}
