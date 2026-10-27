package payrudder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// Purposes of an uploaded file.
const (
	AssetPurposeInvoiceLogo       = "invoice_logo"
	AssetPurposeInvoiceAttachment = "invoice_attachment"
	AssetPurposeGeneral           = "general"
)

// AssetFile is one file of UploadAssets.
type AssetFile struct {
	// Filename is the name stored with the asset.
	Filename string
	// ContentType is the media type of the file, such as image/png. Empty means application/octet-stream.
	ContentType string
	// Purpose is one of the AssetPurpose* values. Empty means general.
	Purpose string
	// Content is read once, completely, before the request is sent.
	Content io.Reader
}

// UploadAssets uploads up to 20 small files (25 MiB each, 100 MiB per request) as one multipart/form-data request
// (POST /v1/assets). A bad file comes back "rejected" in the results and does not fail the others. Use an upload
// session (CreateUploadSession) for larger files.
func (s *AssetsService) UploadAssets(ctx context.Context, files []AssetFile, opts ...RequestOption) (*UploadAssetsResponse, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	purposes := map[string]map[string]string{}
	for _, f := range files {
		if f.Purpose != "" {
			purposes[f.Filename] = map[string]string{"purpose": f.Purpose}
		}
	}
	if len(purposes) > 0 {
		meta, err := json.Marshal(map[string]any{"files": purposes})
		if err != nil {
			return nil, fmt.Errorf("payrudder: encode upload metadata: %w", err)
		}
		if err := mw.WriteField("metadata", string(meta)); err != nil {
			return nil, err
		}
	}
	for _, f := range files {
		ct := f.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, quoteFilename(f.Filename)))
		h.Set("Content-Type", ct)
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, f.Content); err != nil {
			return nil, fmt.Errorf("payrudder: read %s: %w", f.Filename, err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	var out UploadAssetsResponse
	rs := requestSpec{method: http.MethodPost, path: "/v1/assets", rawBody: buf.Bytes(), contentType: mw.FormDataContentType()}
	if err := s.c.do(ctx, rs, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

var filenameEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "")

func quoteFilename(s string) string { return filenameEscaper.Replace(s) }
