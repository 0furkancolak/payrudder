//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func formUpload(t *testing.T, key, meta string, files []struct {
	name, ctype string
	data        []byte
}) result {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if meta != "" {
		fw, _ := mw.CreateFormField("metadata")
		_, _ = fw.Write([]byte(meta))
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, f.name))
		h.Set("Content-Type", f.ctype)
		fw, _ := mw.CreatePart(h)
		_, _ = fw.Write(f.data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", gateway+"/v1/assets", &body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := result{status: resp.StatusCode, header: resp.Header}
	_ = json.NewDecoder(resp.Body).Decode(&r.body)
	return r
}

// TestAssetsEndToEnd uploads through both paths: a direct multipart session straight to MinIO with presigned part URLs,
// and a form-data request carrying 20 files, one of them with a spoofed content type.
func TestAssetsEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey

	// Direct: 20 MiB in 3 parts (8 MiB each, the last smaller).
	data := make([]byte, 20<<20)
	_, _ = rand.Read(data)
	copy(data, "PK\x03\x04")
	r := call(t, "POST", "/v1/assets/uploads", k, map[string]any{"files": []any{
		map[string]any{"filename": "archive.zip", "content_type": "application/zip", "size": len(data), "purpose": "general"}}})
	if r.status != 201 {
		t.Fatalf("create session: %d %v", r.status, r.body)
	}
	sess := r.body["id"].(string)
	file := r.body["files"].([]any)[0].(map[string]any)
	assetID := file["asset_id"].(string)
	parts := file["presigned_parts"].([]any)
	if len(parts) != 3 {
		t.Fatalf("presigned parts = %d, want 3", len(parts))
	}
	partSize := 8 << 20
	var etags []any
	for i, p := range parts {
		lo, hi := i*partSize, min((i+1)*partSize, len(data))
		req, _ := http.NewRequest("PUT", p.(map[string]any)["url"].(string), bytes.NewReader(data[lo:hi]))
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("PUT part %d: %v %v", i+1, resp, err)
		}
		resp.Body.Close()
		etags = append(etags, map[string]any{"part_number": i + 1, "etag": resp.Header.Get("ETag")})
	}
	done := call(t, "POST", fmt.Sprintf("/v1/assets/uploads/%s/files/%s/complete", sess, assetID), k, map[string]any{"parts": etags})
	if done.status != 200 || done.body["status"] != "ASSET_STATUS_READY" || done.body["size_bytes"] != fmt.Sprint(len(data)) {
		t.Fatalf("complete: %d %v", done.status, done.body)
	}
	dl := call(t, "GET", "/v1/assets/"+assetID+"/download", k, nil)
	resp, err := http.Get(dl.body["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, data) || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d, %d bytes, %q", resp.StatusCode, len(got), resp.Header.Get("Content-Disposition"))
	}

	// Form-data: 20 PNGs, one of which is really a PDF.
	var files []struct {
		name, ctype string
		data        []byte
	}
	for i := 0; i < 19; i++ {
		files = append(files, struct {
			name, ctype string
			data        []byte
		}{fmt.Sprintf("img%02d.png", i), "image/png", tinyPNG()})
	}
	files = append(files, struct {
		name, ctype string
		data        []byte
	}{"spoof.png", "image/png", append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 600)...)})
	up := formUpload(t, k, "", files)
	results, _ := up.body["results"].([]any)
	if up.status != 200 || len(results) != 20 {
		t.Fatalf("form upload: %d %v", up.status, up.body)
	}
	created, rejected := 0, 0
	for _, x := range results {
		res := x.(map[string]any)
		switch res["status"] {
		case "created":
			created++
		case "rejected":
			rejected++
			if res["error_code"] != "CONTENT_TYPE_MISMATCH" || res["filename"] != "spoof.png" {
				t.Errorf("rejected = %v", res)
			}
		}
	}
	if created != 19 || rejected != 1 {
		t.Fatalf("created %d, rejected %d; want 19 and 1", created, rejected)
	}
	list := call(t, "GET", "/v1/assets?page_size=100", k, nil)
	if items, _ := list.body["assets"].([]any); len(items) != 20 { // the 20 MiB archive and 19 PNGs
		t.Fatalf("assets listed = %d, want 20", len(items))
	}

	// Another tenant sees none of it.
	other := newAccount(t, "TENANT_TIER_STANDARD")
	if r := call(t, "GET", "/v1/assets/"+assetID, other.liveKey, nil); r.status != 404 {
		t.Fatalf("another tenant's asset: %d %v", r.status, r.body)
	}
	// More than 20 files is refused.
	var many []struct {
		name, ctype string
		data        []byte
	}
	for i := 0; i < 21; i++ {
		many = append(many, struct {
			name, ctype string
			data        []byte
		}{fmt.Sprintf("m%02d.png", i), "image/png", tinyPNG()})
	}
	if r := formUpload(t, k, "", many); r.status != 413 {
		t.Fatalf("21 files: %d %v", r.status, r.body)
	}
}

// TestInvoiceLogoOnThePDF uploads a logo, sets it as the tenant's invoice logo and checks that the sandbox invoice PDF
// carries an image.
func TestInvoiceLogoOnThePDF(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	setupSandbox(t, a, map[string]string{"cus_logo": "pm_sandbox_success"})
	up := formUpload(t, k, `{"files": {"logo.png": {"purpose": "invoice_logo"}}}`, []struct {
		name, ctype string
		data        []byte
	}{{"logo.png", "image/png", tinyPNG()}})
	results, _ := up.body["results"].([]any)
	if up.status != 200 || len(results) != 1 || results[0].(map[string]any)["status"] != "created" {
		t.Fatalf("logo upload: %d %v", up.status, up.body)
	}
	logoID := results[0].(map[string]any)["asset"].(map[string]any)["id"].(string)
	if r := call(t, "PUT", "/v1/tenant/invoice-logo", k, map[string]any{"asset_id": logoID}); r.status != 200 || r.body["invoice_logo_asset_id"] != logoID {
		t.Fatalf("set logo: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/close-period", k, nil); r.status != 202 {
		t.Fatalf("close-period: %d %v", r.status, r.body)
	}
	inv := waitInvoices(t, k, []string{"cus_logo"}, 4*time.Minute)["cus_logo"]
	pdf := call(t, "GET", "/v1/invoices/"+inv["id"].(string)+"/pdf", k, nil)
	resp, err := http.Get(pdf.body["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(b, []byte("/Subtype /Image")) {
		t.Fatal("the invoice PDF has no logo image")
	}
}

// TestAnalyticsEndToEnd streams events into ClickHouse and reads them back through the public API.
func TestAnalyticsEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "tokens", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "tokens"}},
		{"/v1/customers", map[string]any{"external_id": "cus_big", "name": "Big", "email": "big@example.com"}},
		{"/v1/customers", map[string]any{"external_id": "cus_small", "name": "Small", "email": "small@example.com"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	at := time.Now().Add(-10 * time.Minute)
	ev := func(key, cust string, qty int) map[string]any {
		e := event(key, "tokens", qty, at)
		e["customer_external_id"] = cust
		return e
	}
	if r := call(t, "POST", "/v1/events", k, map[string]any{"events": []any{
		ev("a1", "cus_big", 700), ev("a2", "cus_big", 300), ev("a3", "cus_small", 50), ev("a1", "cus_big", 700)}}); r.status != 202 {
		t.Fatalf("events: %d %v", r.status, r.body)
	}
	from, to := at.Add(-time.Hour).UTC().Format(time.RFC3339), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	path := "/v1/analytics/usage?meter=tokens&granularity=hour&from=" + from + "&to=" + to
	var series result
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		series = call(t, "GET", path+"&exact=true", k, nil)
		if pts, _ := series.body["points"].([]any); series.status == 200 && len(pts) > 0 {
			break
		}
		time.Sleep(time.Second)
	}
	pts, _ := series.body["points"].([]any)
	if series.status != 200 || series.body["precision"] != "exact" || len(pts) == 0 {
		t.Fatalf("exact series: %d %v", series.status, series.body)
	}
	var sum, events float64
	for _, p := range pts {
		var q float64
		fmt.Sscan(p.(map[string]any)["quantity"].(string), &q)
		sum += q
		var n float64
		fmt.Sscan(fmt.Sprint(p.(map[string]any)["event_count"]), &n)
		events += n
	}
	if sum != 1050 || events != 3 {
		t.Fatalf("exact series totals %v over %v events, want 1050 over 3 (the duplicate counts once)", sum, events)
	}
	est := call(t, "GET", path, k, nil)
	if est.body["precision"] != "estimated" {
		t.Fatalf("default precision = %v", est.body["precision"])
	}
	top := call(t, "GET", "/v1/analytics/top-customers?meter=tokens&period="+at.UTC().Format("2006-01"), k, nil)
	cs, _ := top.body["customers"].([]any)
	if top.status != 200 || len(cs) != 2 || cs[0].(map[string]any)["customer_external_id"] != "cus_big" {
		t.Fatalf("top customers: %d %v", top.status, top.body)
	}
	other := newAccount(t, "TENANT_TIER_STANDARD")
	if r := call(t, "GET", path+"&exact=true", other.liveKey, nil); r.status != 200 || len(r.body["points"].([]any)) != 0 {
		t.Fatalf("another tenant's series: %d %v", r.status, r.body)
	}
	if r := call(t, "GET", "/admin/v1/analytics/ingest-stats?window_seconds=3600", operatorToken, nil); r.status != 200 || len(r.body["tenants"].([]any)) == 0 {
		t.Fatalf("ingest stats: %d %v", r.status, r.body)
	}
}
