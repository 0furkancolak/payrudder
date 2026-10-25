package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Limits of the proxied form-data upload (docs/03-services/assets-svc.md#b-proxied-multipartform-data).
const (
	maxUploadFiles   = 20
	maxUploadFile    = 25 << 20
	maxUploadRequest = 100 << 20
	uploadChunk      = 256 << 10
	// formOverhead leaves room for multipart framing and the metadata part on top of the file bytes.
	formOverhead = 1 << 20
)

// bodyLimit is the request body limit of a route: 1 MiB, except the form-data upload.
func bodyLimit(method, path string) int64 {
	if method == http.MethodPost && path == "/v1/assets" {
		return maxUploadRequest + formOverhead
	}
	return MaxBodyBytes
}

func purposeOf(s string) assetsv1.Purpose {
	switch s {
	case "invoice_logo":
		return assetsv1.Purpose_PURPOSE_INVOICE_LOGO
	case "invoice_attachment":
		return assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT
	case "general", "":
		return assetsv1.Purpose_PURPOSE_GENERAL
	}
	return assetsv1.Purpose_PURPOSE_UNSPECIFIED
}

type fileSpecJSON struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Purpose     string `json:"purpose"`
	SHA256      string `json:"sha256"`
}

// createUploadSession is POST /v1/assets/uploads.
func (s *Server) createUploadSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files []fileSpecJSON `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, invalid("body", "must be JSON like {\"files\": [{\"filename\": …, \"content_type\": …, \"size\": …, \"purpose\": …}]}"))
		return
	}
	req := &assetsv1.CreateUploadSessionRequest{IdempotencyKey: r.Header.Get("Idempotency-Key")}
	for i, f := range body.Files {
		p := purposeOf(f.Purpose)
		if p == assetsv1.Purpose_PURPOSE_UNSPECIFIED {
			writeError(w, r, invalid("files["+strconv.Itoa(i)+"].purpose", "invoice_logo, invoice_attachment or general"))
			return
		}
		req.Files = append(req.Files, &assetsv1.FileSpec{Filename: f.Filename, ContentType: f.ContentType, Size: f.Size, Purpose: p, Sha256: f.SHA256})
	}
	resp, err := s.d.Assets.CreateUploadSession(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetSession(), err)
}

func (s *Server) presignParts(w http.ResponseWriter, r *http.Request) {
	req := &assetsv1.PresignPartsRequest{SessionId: r.PathValue("session"), AssetId: r.PathValue("asset"), FromPart: 1, Count: 100}
	for name, dst := range map[string]*int32{"from": &req.FromPart, "count": &req.Count} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				writeError(w, r, invalid(name, "must be an integer"))
				return
			}
			*dst = int32(n)
		}
	}
	resp, err := s.d.Assets.PresignParts(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getUploadFile(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.GetUploadFile(r.Context(), &assetsv1.GetUploadFileRequest{SessionId: r.PathValue("session"), AssetId: r.PathValue("asset")})
	respond(w, r, http.StatusOK, resp.GetFile(), err)
}

func (s *Server) completeUploadFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parts []struct {
			PartNumber int32  `json:"part_number"`
			ETag       string `json:"etag"`
		} `json:"parts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, invalid("body", "must be JSON like {\"parts\": [{\"part_number\": 1, \"etag\": \"…\"}]}"))
		return
	}
	req := &assetsv1.CompleteFileRequest{SessionId: r.PathValue("session"), AssetId: r.PathValue("asset")}
	for _, p := range body.Parts {
		req.Parts = append(req.Parts, &assetsv1.PartETag{PartNumber: p.PartNumber, Etag: p.ETag})
	}
	resp, err := s.d.Assets.CompleteFile(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetAsset(), err)
}

func (s *Server) abortUploadSession(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.AbortUpload(r.Context(), &assetsv1.AbortUploadRequest{SessionId: r.PathValue("session")})
	respond(w, r, http.StatusOK, resp.GetSession(), err)
}

func (s *Server) abortUploadFile(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.AbortUpload(r.Context(), &assetsv1.AbortUploadRequest{SessionId: r.PathValue("session"), AssetId: r.PathValue("asset")})
	respond(w, r, http.StatusOK, resp.GetSession(), err)
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	req := &assetsv1.ListAssetsRequest{PageToken: r.URL.Query().Get("page_token")}
	if v := r.URL.Query().Get("purpose"); v != "" {
		if req.Purpose = purposeOf(v); req.Purpose == assetsv1.Purpose_PURPOSE_UNSPECIFIED {
			writeError(w, r, invalid("purpose", "invoice_logo, invoice_attachment or general"))
			return
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Assets.ListAssets(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.GetAsset(r.Context(), &assetsv1.GetAssetRequest{AssetId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetAsset(), err)
}

func (s *Server) downloadAsset(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.GetDownloadUrl(r.Context(), &assetsv1.GetDownloadUrlRequest{AssetId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"url": resp.GetUrl(), "expires_at": resp.GetExpireTime().AsTime().UTC().Format("2006-01-02T15:04:05Z07:00")})
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Assets.DeleteAsset(r.Context(), &assetsv1.DeleteAssetRequest{AssetId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetAsset(), err)
}

// uploadResult is one file of a form-data upload.
type uploadResult struct {
	Filename  string          `json:"filename"`
	Status    string          `json:"status"` // "created" | "rejected"
	Asset     json.RawMessage `json:"asset,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Message   string          `json:"message,omitempty"`
}

// errReason extracts the application error code of a gRPC status.
func errReason(err error) (code, msg string) {
	st := status.Convert(err)
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok {
			return ei.GetReason(), st.Message()
		}
	}
	return codeName(st.Code()), st.Message()
}

// uploadAssets is POST /v1/assets (multipart/form-data): up to 20 files of up to 25 MiB, 100 MiB in all. Each file is
// streamed part by part into assets-svc over gRPC, so memory stays bounded by one chunk; a bad file is rejected on its
// own without failing the others. Optional first part "metadata": {"files": {"logo.png": {"purpose": "invoice_logo"}}}.
func (s *Server) uploadAssets(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, invalid("content-type", "multipart/form-data with a boundary is required"))
		return
	}
	p := principalFrom(r.Context())
	meta := map[string]struct {
		Purpose string `json:"purpose"`
	}{}
	results := []uploadResult{}
	var total int64
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, r, errTooLarge{limit: mbe.Limit})
				return
			}
			writeError(w, r, invalid("body", "malformed multipart body"))
			return
		}
		if part.FormName() == "metadata" {
			var m struct {
				Files map[string]struct {
					Purpose string `json:"purpose"`
				} `json:"files"`
			}
			if err := json.NewDecoder(io.LimitReader(part, 64<<10)).Decode(&m); err != nil {
				writeError(w, r, invalid("metadata", "must be JSON like {\"files\": {\"logo.png\": {\"purpose\": \"invoice_logo\"}}}"))
				return
			}
			meta = m.Files
			continue
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		files++
		if files > maxUploadFiles {
			writeError(w, r, errTooLarge{limit: maxUploadFiles, files: true})
			return
		}
		if files > 1 && !s.charge(w, r, p, "write", 1) { // the route charged the first file; each further file costs one more
			return
		}
		res, over := s.uploadOne(r.Context(), part, purposeOf(meta[part.FileName()].Purpose), &total)
		results = append(results, res)
		if over {
			writeError(w, r, errTooLarge{limit: maxUploadRequest})
			return
		}
	}
	if files == 0 {
		writeError(w, r, invalid("file", "no file part"))
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"results": results})
}

// uploadOne streams one form part into assets-svc. over reports that the request-wide limit was exceeded.
func (s *Server) uploadOne(ctx context.Context, part *multipart.Part, purpose assetsv1.Purpose, total *int64) (res uploadResult, over bool) {
	res = uploadResult{Filename: part.FileName()}
	reject := func(code, msg string) uploadResult {
		res.Status, res.ErrorCode, res.Message = "rejected", code, msg
		return res
	}
	ctype := part.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	// Cancelling the stream context is how the gateway tells assets-svc to abort: a plain half-close would look like a
	// complete (but truncated) file.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := s.d.Assets.UploadAsset(sctx)
	if err != nil {
		code, msg := errReason(err)
		return reject(code, msg), false
	}
	if err := stream.Send(&assetsv1.UploadAssetRequest{Msg: &assetsv1.UploadAssetRequest_Header{Header: &assetsv1.UploadHeader{
		Filename: part.FileName(), ContentType: ctype, Purpose: purpose}}}); err != nil && !errors.Is(err, io.EOF) {
		code, msg := errReason(err)
		return reject(code, msg), false
	}
	buf := make([]byte, uploadChunk)
	var size int64
	serverDone := false
	for !serverDone {
		n, rerr := part.Read(buf)
		if n > 0 {
			size += int64(n)
			*total += int64(n)
			switch {
			case *total > maxUploadRequest:
				cancel()
				return reject("REQUEST_TOO_LARGE", "the request exceeds 100 MiB"), true
			case size > maxUploadFile:
				cancel()
				_, _ = io.Copy(io.Discard, part)
				return reject("FILE_TOO_LARGE", "files in a form upload are limited to 25 MiB; use an upload session for larger files"), false
			}
			if serr := stream.Send(&assetsv1.UploadAssetRequest{Msg: &assetsv1.UploadAssetRequest_Chunk{Chunk: buf[:n]}}); serr != nil {
				if errors.Is(serr, io.EOF) {
					serverDone = true // assets-svc answered early (a rejected file): fetch its answer below and skip the rest
				} else {
					cancel()
					code, msg := errReason(serr)
					return reject(code, msg), false
				}
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				cancel() // the client went away mid-part
				return reject("UPLOAD_INTERRUPTED", "the request body ended unexpectedly"), false
			}
			break
		}
	}
	resp, err := stream.CloseAndRecv()
	asset := resp.GetAsset()
	if serverDone {
		_, _ = io.Copy(io.Discard, part)
	}
	if err != nil {
		code, msg := errReason(err)
		return reject(code, msg), false
	}
	if asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
		return reject(asset.GetRejectReason(), "the file was not accepted"), false
	}
	raw, _ := protojson.MarshalOptions{UseProtoNames: true}.Marshal(asset)
	res.Status, res.Asset = "created", raw
	return res, false
}

// setInvoiceLogo is PUT /v1/tenant/invoice-logo {"asset_id": "…"}: the READY invoice_logo asset invoices are drawn with.
// The gateway checks the asset belongs to the tenant (assets-svc answers NOT_FOUND for anyone else's), then makes the one
// operator-level change it is allowed to make on the tenant's behalf.
func (s *Server) setInvoiceLogo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AssetID string `json:"asset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AssetID == "" {
		writeError(w, r, invalid("asset_id", "required"))
		return
	}
	ar, err := s.d.Assets.GetAsset(r.Context(), &assetsv1.GetAssetRequest{AssetId: body.AssetID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	a := ar.GetAsset()
	if a.GetPurpose() != assetsv1.Purpose_PURPOSE_INVOICE_LOGO || a.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
		writeError(w, r, errkey.New(codes.FailedPrecondition, "gateway.assets.bad_invoice_logo", "the asset must be a READY invoice_logo"))
		return
	}
	s.updateLogo(w, r, &plansv1.UpdateTenantSettingsRequest{InvoiceLogoAssetId: a.GetId()})
}

// clearInvoiceLogo is DELETE /v1/tenant/invoice-logo.
func (s *Server) clearInvoiceLogo(w http.ResponseWriter, r *http.Request) {
	s.updateLogo(w, r, &plansv1.UpdateTenantSettingsRequest{ClearInvoiceLogo: true})
}

func (s *Server) updateLogo(w http.ResponseWriter, r *http.Request, req *plansv1.UpdateTenantSettingsRequest) {
	req.TenantId = principalFrom(r.Context()).Tenant.ID.String()
	resp, err := s.d.Admin.UpdateTenantSettings(tenant.WithOperator(r.Context()), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]string{"invoice_logo_asset_id": resp.GetTenant().GetInvoiceLogoAssetId()})
}
