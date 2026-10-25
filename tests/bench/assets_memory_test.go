//go:build bench

package bench

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	gateway  = envOr("PAYRUDDER_GATEWAY_URL", "http://localhost:42800")
	operator = envOr("PAYRUDDER_OPERATOR_TOKEN", "local-operator-token-change-me-0123456789")
)

func api(t *testing.T, method, path, token string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, gateway+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func liveKey(t *testing.T) string {
	t.Helper()
	var acct struct {
		Tenants []struct{ ID, Environment string } `json:"tenants"`
	}
	if c := api(t, "POST", "/admin/v1/accounts", operator, map[string]any{"name": "bench-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		"currency": "TRY", "timezone": "Europe/Istanbul", "grace_period": "0s", "tier": "TENANT_TIER_ENTERPRISE"}, &acct); c != 201 {
		t.Fatalf("account: %d", c)
	}
	for _, tn := range acct.Tenants {
		if tn.Environment == "ENVIRONMENT_LIVE" {
			var k struct{ Plaintext string }
			// A live tenant is created inactive and has no keys: activate it the way support does, with a reason.
			if c := api(t, "PUT", "/admin/v1/tenants/"+tn.ID+"/status", operator, map[string]string{"status": "active", "reason": "bench"}, nil); c != 200 {
				t.Fatalf("activate: %d", c)
			}
			api(t, "POST", "/admin/v1/tenants/"+tn.ID+"/api-keys", operator, nil, &k)
			return k.Plaintext
		}
	}
	t.Fatal("no live tenant")
	return ""
}

// memMiB reads a container's memory usage (docker stats), in MiB.
func memMiB(container string) float64 {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", container).Output()
	if err != nil {
		return -1
	}
	use, _, _ := strings.Cut(strings.TrimSpace(string(out)), " / ")
	switch {
	case strings.HasSuffix(use, "GiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(use, "GiB"), 64)
		return v * 1024
	case strings.HasSuffix(use, "MiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(use, "MiB"), 64)
		return v
	case strings.HasSuffix(use, "KiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(use, "KiB"), 64)
		return v / 1024
	}
	return -1
}

// sampler records the peak memory of containers until stopped.
type sampler struct {
	stop chan struct{}
	wg   sync.WaitGroup
	mu   sync.Mutex
	peak map[string]float64
}

func startSampler(containers ...string) *sampler {
	s := &sampler{stop: make(chan struct{}), peak: map[string]float64{}}
	for _, c := range containers {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				if v := memMiB(c); v > 0 {
					s.mu.Lock()
					s.peak[c] = max(s.peak[c], v)
					s.mu.Unlock()
				}
				select {
				case <-s.stop:
					return
				case <-time.After(300 * time.Millisecond):
				}
			}
		}()
	}
	return s
}

func (s *sampler) finish() map[string]float64 {
	close(s.stop)
	s.wg.Wait()
	return s.peak
}

func formRequest(key string, n, size int) (*http.Request, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	payload := make([]byte, size)
	for i := 0; i < n; i++ {
		_, _ = rand.Read(payload)
		copy(payload, "\x89PNG\r\n\x1a\n")
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="f%02d.png"`, i))
		h.Set("Content-Type", "image/png")
		fw, _ := mw.CreatePart(h)
		_, _ = fw.Write(payload)
	}
	_ = mw.Close()
	req, err := http.NewRequest("POST", gateway+"/v1/assets", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, nil
}

// TestFormUploadGatewayMemory: 20 x 1 MiB files per request (docs/03-services/assets-svc.md#scaling-notes, target:
// gateway RSS growth < 16 MiB), first one request at a time, then four at once.
func TestFormUploadGatewayMemory(t *testing.T) {
	key := liveKey(t)
	do := func() {
		req, err := formRequest(key, 20, 1<<20)
		if err != nil {
			t.Error(err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("form upload: %d", resp.StatusCode)
		}
	}
	for i := 0; i < 3; i++ { // warm up: allocator and connection pools
		do()
	}
	time.Sleep(2 * time.Second)
	base := memMiB("payrudder-gateway-1")
	s := startSampler("payrudder-gateway-1", "payrudder-assets-svc-1")
	for i := 0; i < 15; i++ {
		do()
	}
	seqPeak := s.finish()
	s = startSampler("payrudder-gateway-1", "payrudder-assets-svc-1")
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				do()
			}
		}()
	}
	wg.Wait()
	conPeak := s.finish()
	t.Logf("gateway RSS before: %.1f MiB; peak over 15 sequential 20x1MiB requests: %.1f MiB (growth %.1f); peak over 4x8 concurrent: %.1f MiB (growth %.1f); assets-svc peaks %.1f / %.1f MiB",
		base, seqPeak["payrudder-gateway-1"], seqPeak["payrudder-gateway-1"]-base, conPeak["payrudder-gateway-1"], conPeak["payrudder-gateway-1"]-base,
		seqPeak["payrudder-assets-svc-1"], conPeak["payrudder-assets-svc-1"])
}

// TestDirectUploadMemory: 1 GiB straight to object storage with 8 parallel parts (target: assets-svc memory <= 64 MiB).
func TestDirectUploadMemory(t *testing.T) {
	key := liveKey(t)
	const size = 1 << 30
	var sess struct {
		ID    string `json:"id"`
		Files []struct {
			AssetID        string `json:"asset_id"`
			PartSize       string `json:"part_size"`
			PartCount      int    `json:"part_count"`
			PresignedParts []struct {
				URL string `json:"url"`
			} `json:"presigned_parts"`
		} `json:"files"`
	}
	if c := api(t, "POST", "/v1/assets/uploads", key, map[string]any{"files": []any{map[string]any{"filename": "big.zip", "content_type": "application/zip", "size": size, "purpose": "general"}}}, &sess); c != 201 {
		t.Fatalf("session: %d", c)
	}
	f := sess.Files[0]
	parts := f.PartCount
	partSize, err := strconv.ParseInt(f.PartSize, 10, 64)
	if err != nil || partSize <= 0 || parts == 0 {
		t.Fatalf("session has part_size %q, part_count %d", f.PartSize, parts)
	}
	urls := make([]string, 0, parts)
	for _, p := range f.PresignedParts {
		urls = append(urls, p.URL)
	}
	// A session carries the URLs of the first 100 parts; the rest are asked for 100 at a time.
	for len(urls) < parts {
		var more struct {
			Parts []struct {
				URL string `json:"url"`
			} `json:"parts"`
		}
		if c := api(t, "POST", fmt.Sprintf("/v1/assets/uploads/%s/files/%s/parts?from=%d&count=100", sess.ID, f.AssetID, len(urls)+1), key, nil, &more); c != 200 || len(more.Parts) == 0 {
			t.Fatalf("more parts from %d: %d", len(urls)+1, c)
		}
		for _, p := range more.Parts {
			urls = append(urls, p.URL)
		}
	}
	base := memMiB("payrudder-assets-svc-1")
	s := startSampler("payrudder-assets-svc-1", "payrudder-gateway-1")
	start := time.Now()
	etags := make([]any, parts)
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, partSize)
			_, _ = rand.Read(buf)
			buf[0], buf[1], buf[2], buf[3] = 'P', 'K', 3, 4
			for {
				i := int(next.Add(1)) - 1
				if i >= parts {
					return
				}
				n := min(partSize, int64(size)-int64(i)*partSize)
				req, _ := http.NewRequest("PUT", urls[i], bytes.NewReader(buf[:n]))
				resp, err := http.DefaultClient.Do(req)
				if err != nil || resp.StatusCode != 200 {
					t.Errorf("PUT part %d: %v %v", i+1, resp, err)
					return
				}
				_ = resp.Body.Close()
				etags[i] = map[string]any{"part_number": i + 1, "etag": resp.Header.Get("ETag")}
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)
	var done map[string]any
	c := api(t, "POST", fmt.Sprintf("/v1/assets/uploads/%s/files/%s/complete", sess.ID, f.AssetID), key, map[string]any{"parts": etags}, &done)
	peaks := s.finish()
	if c != 200 || done["status"] != "ASSET_STATUS_READY" {
		t.Fatalf("complete: %d %v", c, done)
	}
	t.Logf("1 GiB in %d parts of %d MiB, 8 in parallel, %s (%.0f MiB/s); assets-svc memory before %.1f MiB, peak %.1f MiB; gateway peak %.1f MiB",
		parts, partSize>>20, took.Round(time.Millisecond), 1024/took.Seconds(), base, peaks["payrudder-assets-svc-1"], peaks["payrudder-gateway-1"])
}
