package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
)

type fakeAssets struct {
	assetsv1.UnimplementedAssetsServiceServer
}

func (fakeAssets) CreateUploadSession(_ context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.CreateUploadSessionResponse, error) {
	if len(req.GetFiles()) == 1 && req.GetFiles()[0].GetSize() > 1<<40 {
		st, _ := status.New(codes.FailedPrecondition, "the files do not fit in the tenant's storage quota").WithDetails(&errdetails.ErrorInfo{Reason: "STORAGE_QUOTA_EXCEEDED"})
		return nil, st.Err()
	}
	s := &assetsv1.UploadSession{Id: "sess-1", Status: "OPEN", ExpireTime: timestamppb.Now()}
	for i, f := range req.GetFiles() {
		s.Files = append(s.Files, &assetsv1.UploadFile{AssetId: fmt.Sprintf("a%d", i), Filename: f.GetFilename(), Size: f.GetSize(), PartCount: 1,
			PresignedParts: []*assetsv1.PresignedPart{{PartNumber: 1, Url: "http://s3/part1"}}})
	}
	if req.GetIdempotencyKey() == "idem" {
		s.Id = "sess-idem"
	}
	return &assetsv1.CreateUploadSessionResponse{Session: s}, nil
}

func (fakeAssets) CompleteFile(_ context.Context, req *assetsv1.CompleteFileRequest) (*assetsv1.CompleteFileResponse, error) {
	return &assetsv1.CompleteFileResponse{Asset: &assetsv1.Asset{Id: req.GetAssetId(), SessionId: req.GetSessionId(), Status: assetsv1.AssetStatus_ASSET_STATUS_READY, SizeBytes: int64(len(req.GetParts()))}}, nil
}

func (fakeAssets) GetAsset(_ context.Context, req *assetsv1.GetAssetRequest) (*assetsv1.GetAssetResponse, error) {
	if req.GetAssetId() == "missing" {
		return nil, status.Error(codes.NotFound, "asset not found")
	}
	return &assetsv1.GetAssetResponse{Asset: &assetsv1.Asset{Id: req.GetAssetId(), Filename: "a.png", Status: assetsv1.AssetStatus_ASSET_STATUS_READY}}, nil
}

func (fakeAssets) GetDownloadUrl(context.Context, *assetsv1.GetDownloadUrlRequest) (*assetsv1.GetDownloadUrlResponse, error) {
	return &assetsv1.GetDownloadUrlResponse{Url: "http://s3/dl?sig=1", ExpireTime: timestamppb.Now()}, nil
}

func (fakeAssets) UploadAsset(stream grpc.ClientStreamingServer[assetsv1.UploadAssetRequest, assetsv1.UploadAssetResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	h := first.GetHeader()
	if strings.HasPrefix(h.GetFilename(), "spoof") {
		return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: &assetsv1.Asset{Id: "rejected", Filename: h.GetFilename(), Status: assetsv1.AssetStatus_ASSET_STATUS_REJECTED, RejectReason: "CONTENT_TYPE_MISMATCH"}})
	}
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
	return stream.SendAndClose(&assetsv1.UploadAssetResponse{Asset: &assetsv1.Asset{Id: "asset-" + h.GetFilename(), Filename: h.GetFilename(), ContentType: h.GetContentType(), Purpose: h.GetPurpose(),
		SizeBytes: n, Status: assetsv1.AssetStatus_ASSET_STATUS_READY}})
}

func form(t *testing.T, meta string, files map[string]int) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if meta != "" {
		fw, _ := mw.CreateFormField("metadata")
		_, _ = fw.Write([]byte(meta))
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	for i := range len(names) { // stable order
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, n))
		h.Set("Content-Type", "image/png")
		fw, _ := mw.CreatePart(h)
		_, _ = fw.Write(bytes.Repeat([]byte{'x'}, files[n]))
	}
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

func postForm(t *testing.T, ts string, key string, body io.Reader, ctype string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts+"/v1/assets", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func TestFormUploadPerFileResults(t *testing.T) {
	ts, _ := newTestServer(t)
	files := map[string]int{}
	for i := 0; i < 19; i++ {
		files[fmt.Sprintf("f%02d.png", i)] = 1000 + i
	}
	files["spoof.png"] = 500
	body, ctype := form(t, `{"files": {"f00.png": {"purpose": "invoice_logo"}}}`, files)
	resp, m := postForm(t, ts.URL, "live-key", body, ctype)
	results, _ := m["results"].([]any)
	if resp.StatusCode != 200 || len(results) != 20 {
		t.Fatalf("upload: %d %v", resp.StatusCode, m)
	}
	created, rejected := 0, 0
	for _, r := range results {
		res := r.(map[string]any)
		switch res["status"] {
		case "created":
			created++
		case "rejected":
			rejected++
			if res["filename"] != "spoof.png" || res["error_code"] != "CONTENT_TYPE_MISMATCH" {
				t.Errorf("rejected = %v", res)
			}
		}
	}
	if created != 19 || rejected != 1 {
		t.Fatalf("created %d, rejected %d; want 19 and 1", created, rejected)
	}
	first := results[0].(map[string]any)
	asset := first["asset"].(map[string]any)
	if asset["purpose"] != "PURPOSE_INVOICE_LOGO" || asset["size_bytes"] != "1000" {
		t.Fatalf("first asset = %v (the metadata part picks the purpose, the stream carries every byte)", asset)
	}
}

func TestFormUploadLimits(t *testing.T) {
	ts, _ := newTestServer(t)
	// A 21st file is refused.
	files := map[string]int{}
	for i := 0; i < 21; i++ {
		files[fmt.Sprintf("f%02d.png", i)] = 10
	}
	body, ctype := form(t, "", files)
	if resp, m := postForm(t, ts.URL, "live-key", body, ctype); resp.StatusCode != 413 || errCode(m) != "REQUEST_TOO_LARGE" {
		t.Fatalf("21 files: %d %v", resp.StatusCode, m)
	}
	// A file over 25 MiB is rejected on its own; the small one beside it is created.
	body, ctype = form(t, "", map[string]int{"big.png": 26 << 20, "small.png": 100})
	resp, m := postForm(t, ts.URL, "live-key", body, ctype)
	results, _ := m["results"].([]any)
	if resp.StatusCode != 200 || len(results) != 2 || results[0].(map[string]any)["error_code"] != "FILE_TOO_LARGE" || results[1].(map[string]any)["status"] != "created" {
		t.Fatalf("big + small: %d %v", resp.StatusCode, m)
	}
	// More than 100 MiB in all.
	body, ctype = form(t, "", map[string]int{"a.png": 25 << 20, "b.png": 25 << 20, "c.png": 25 << 20, "d.png": 25 << 20, "e.png": 25 << 20})
	if resp, _ := postForm(t, ts.URL, "live-key", body, ctype); resp.StatusCode != 413 {
		t.Fatalf("125 MiB: %d, want 413", resp.StatusCode)
	}
	if resp, m := do(t, ts, "POST", "/v1/assets", "live-key", "not multipart"); resp.StatusCode != 400 || errCode(m) != "INVALID_ARGUMENT" {
		t.Fatalf("plain body: %d %v", resp.StatusCode, m)
	}
}

func TestDirectUploadRoutes(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/assets/uploads", "live-key", `{"files":[{"filename":"a.zip","content_type":"application/zip","size":1024,"purpose":"general"}]}`)
	files, _ := m["files"].([]any)
	if resp.StatusCode != 201 || len(files) != 1 || files[0].(map[string]any)["presigned_parts"] == nil {
		t.Fatalf("create: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/assets/uploads", "live-key", `{"files":[{"filename":"a","content_type":"x","size":1,"purpose":"weird"}]}`); resp.StatusCode != 400 {
		t.Fatalf("bad purpose: %d %v", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "POST", "/v1/assets/uploads", "live-key", `{"files":[{"filename":"a.zip","content_type":"application/zip","size":2000000000000,"purpose":"general"}]}`)
	if resp.StatusCode != 409 || errCode(m) != "STORAGE_QUOTA_EXCEEDED" {
		t.Fatalf("quota: %d %v (the application error code must reach the envelope)", resp.StatusCode, m)
	}
	resp, m = do(t, ts, "POST", "/v1/assets/uploads/sess-1/files/a0/complete", "live-key", `{"parts":[{"part_number":1,"etag":"abc"}]}`)
	if resp.StatusCode != 200 || m["status"] != "ASSET_STATUS_READY" {
		t.Fatalf("complete: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/assets/missing", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("missing asset: %d", resp.StatusCode)
	}
	resp, m = do(t, ts, "GET", "/v1/assets/a1/download", "live-key", "")
	if resp.StatusCode != 200 || m["url"] == "" {
		t.Fatalf("download: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/assets/uploads/sess-1/files/a0", "", ""); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
}

func TestInvoiceLogoNeedsALogoAsset(t *testing.T) {
	ts, _ := newTestServer(t)
	// The fake asset is a READY general file, not an invoice_logo.
	if resp, m := do(t, ts, "PUT", "/v1/tenant/invoice-logo", "live-key", `{"asset_id":"a1"}`); resp.StatusCode != 409 || errCode(m) != "FAILED_PRECONDITION" {
		t.Fatalf("wrong purpose: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "PUT", "/v1/tenant/invoice-logo", "live-key", `{}`); resp.StatusCode != 400 {
		t.Fatalf("no asset id: %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, "PUT", "/v1/tenant/invoice-logo", "live-key", `{"asset_id":"missing"}`); resp.StatusCode != 404 {
		t.Fatalf("unknown asset: %d", resp.StatusCode)
	}
}
