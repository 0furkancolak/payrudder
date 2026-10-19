//go:build chaos

package chaos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func toxiCall(base, method, path string, body any) error {
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, base+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("toxiproxy %s %s: %d", method, path, resp.StatusCode)
	}
	return nil
}
