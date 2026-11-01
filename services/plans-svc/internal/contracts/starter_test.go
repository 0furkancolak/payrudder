package contracts

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Every starter text exists for every kind in Turkish and English, begins with the notice that it is a template, passes the same checks a seller's text does, and
// leaves a placeholder for what only the seller knows. No other language has one: a machine translation of a legal text is not offered.
func TestStarterTexts(t *testing.T) {
	for _, kind := range domain.ContractKinds {
		for _, loc := range []string{"tr", "en"} {
			body, served, ok := Starter(kind, loc)
			if !ok || served != loc {
				t.Fatalf("no starter %s/%s", kind, loc)
			}
			if !domain.HasTemplateNotice(body) || !(strings.HasPrefix(body, "**"+domain.NoticeEN) || strings.HasPrefix(body, "**"+domain.NoticeTR)) {
				t.Errorf("%s/%s does not begin with the template notice", kind, loc)
			}
			if _, err := domain.ValidateContractBodies(map[string]string{loc: body}); err != nil {
				t.Errorf("%s/%s fails the checks of a seller's text: %v", kind, loc, err)
			}
			if !strings.Contains(body, "[seller:") && !strings.Contains(body, "[satıcı:") && !strings.Contains(body, "[we:") && !strings.Contains(body, "[biz:") {
				t.Errorf("%s/%s leaves nothing for the seller to fill in", kind, loc)
			}
			if kind == domain.ContractPreInfo && !strings.Contains(body, "{{seller_tax_id}}") {
				t.Errorf("%s/%s lacks the seller's identity (TR art. 5(1)(b), EU art. 6(1)(b))", kind, loc)
			}
		}
		for _, loc := range []string{"de", "fr", "es", "ar", ""} {
			if _, _, ok := Starter(kind, loc); ok {
				t.Errorf("a starter text in %q exists: only Turkish and English are offered", loc)
			}
		}
	}
	if _, _, ok := Starter("nda", "en"); ok {
		t.Error("a starter for a kind that does not exist")
	}
}
