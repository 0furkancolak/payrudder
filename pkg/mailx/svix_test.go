package mailx

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The vector is the one the Svix page prints (https://docs.svix.com/receiving/verifying-payloads/how-manual, read 2026-11-02), computed by Svix, not by this code.
const (
	vectorSecret = "whsec_plJ3nmyCDGBKInavdOK15jsl"
	vectorID     = "msg_loFOjxBNrRLzqYUf"
	vectorTime   = 1731705121
	vectorBody   = `{"event_type":"ping","data":{"success":true}}`
	vectorSig    = "v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0="
)

func vectorHeaders(sig string) http.Header {
	h := http.Header{}
	h.Set("svix-id", vectorID)
	h.Set("svix-timestamp", strconv.Itoa(vectorTime))
	h.Set("svix-signature", sig)
	return h
}

func TestSvixSignsLikeSvix(t *testing.T) {
	got, err := SvixSign(vectorSecret, vectorID, time.Unix(vectorTime, 0), []byte(vectorBody))
	if err != nil || got != vectorSig {
		t.Fatalf("signature %q, %v; Svix's page says %q", got, err, vectorSig)
	}
}

func TestVerifySvix(t *testing.T) {
	at := time.Unix(vectorTime, 0)
	if err := VerifySvix(vectorHeaders(vectorSig), []byte(vectorBody), vectorSecret, at, SvixTolerance); err != nil {
		t.Fatalf("Svix's own vector: %v", err)
	}
	// Any one of several signatures (a secret being rotated sends two) is enough.
	if err := VerifySvix(vectorHeaders("v1,AAAA "+vectorSig), []byte(vectorBody), vectorSecret, at, SvixTolerance); err != nil {
		t.Errorf("the second of two signatures: %v", err)
	}
	for name, c := range map[string]struct {
		h      http.Header
		body   string
		secret string
		now    time.Time
	}{
		"a changed body":        {vectorHeaders(vectorSig), vectorBody + " ", vectorSecret, at},
		"another secret":        {vectorHeaders(vectorSig), vectorBody, "whsec_" + "dGhpcyBpcyBhbm90aGVyIHNlY3JldA==", at},
		"a v2 signature":        {vectorHeaders("v2," + vectorSig[3:]), vectorBody, vectorSecret, at},
		"no signature":          {vectorHeaders(""), vectorBody, vectorSecret, at},
		"a stale timestamp":     {vectorHeaders(vectorSig), vectorBody, vectorSecret, at.Add(SvixTolerance + time.Second)},
		"a timestamp too early": {vectorHeaders(vectorSig), vectorBody, vectorSecret, at.Add(-SvixTolerance - time.Second)},
		"a secret with no form": {vectorHeaders(vectorSig), vectorBody, "plJ3nmyCDGBKInavdOK15jsl", at},
	} {
		if err := VerifySvix(c.h, []byte(c.body), c.secret, c.now, SvixTolerance); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	h := vectorHeaders(vectorSig)
	h.Del("svix-id")
	if err := VerifySvix(h, []byte(vectorBody), vectorSecret, at, SvixTolerance); err == nil {
		t.Error("a request with no svix-id was accepted")
	}
}

func TestValidSvixSecret(t *testing.T) {
	if err := ValidSvixSecret(vectorSecret); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "whsec_", "whsec_!!!", "whsec_AAAA", "secret"} {
		if ValidSvixSecret(bad) == nil {
			t.Errorf("%q is not a signing secret", bad)
		}
	}
}
