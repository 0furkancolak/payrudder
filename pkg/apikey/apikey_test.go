package apikey_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/apikey"
)

func TestGenerateParseRoundTrip(t *testing.T) {
	for _, env := range []apikey.Env{apikey.EnvTest, apikey.EnvLive} {
		k, err := apikey.Generate(env)
		if err != nil {
			t.Fatal(err)
		}
		s := k.String()
		if !strings.HasPrefix(s, "qk_"+string(env)+"_") || len(k.Secret) != 43 {
			t.Fatalf("unexpected key shape %q", s)
		}
		p, err := apikey.Parse(s)
		if err != nil || p != k {
			t.Fatalf("Parse(%q) = %+v, %v", s, p, err)
		}
		if !p.Matches(k.Hash()) {
			t.Fatal("hash mismatch")
		}
		other, _ := apikey.Generate(env)
		if p.Matches(other.Hash()) {
			t.Fatal("different key matched")
		}
	}
}

func TestParseRejects(t *testing.T) {
	good, _ := apikey.Generate(apikey.EnvLive)
	for _, bad := range []string{
		"",
		"qk_prod_" + good.Prefix + "_" + good.Secret,
		"qk_live_short_" + good.Secret,
		"qk_live_" + good.Prefix + "_tooShort",
		"qk_live_" + good.Prefix + "_" + good.Secret + "_x",
		"sk_live_" + good.Prefix + "_" + good.Secret,
		"qk_live_" + good.Prefix + "_" + strings.Repeat("!", 43),
	} {
		if _, err := apikey.Parse(bad); !errors.Is(err, apikey.ErrMalformed) {
			t.Errorf("Parse(%q) err = %v", bad, err)
		}
	}
}

func TestCacheKeyIsTheGatewaysKey(t *testing.T) {
	if got := apikey.CacheKey("abcd1234"); got != "gw:apikey:abcd1234" {
		t.Errorf("CacheKey = %q; the gateway cache and docs/03-services/gateway.md say gw:apikey:{prefix}", got)
	}
}
