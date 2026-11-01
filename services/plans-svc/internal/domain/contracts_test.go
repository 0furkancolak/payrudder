package domain

import (
	"strings"
	"testing"
	"time"
)

func TestContractVariablesIn(t *testing.T) {
	used, unknown := ContractVariablesIn("Hi {{customer_name}}, {{ seller_name }} and {{customer_name}} again; {{secret}} and a {{ stray")
	if strings.Join(used, ",") != "customer_name,seller_name" {
		t.Errorf("used = %v", used)
	}
	if strings.Join(unknown, ",") != "{{ }},secret" {
		t.Errorf("unknown = %v", unknown)
	}
	if u, k := ContractVariablesIn("no variables, 100% {braces}"); len(u) != 0 || len(k) != 0 {
		t.Errorf("plain text: %v %v", u, k)
	}
}

func TestValidateContractBodies(t *testing.T) {
	if _, err := ValidateContractBodies(nil); err == nil {
		t.Error("no text is refused")
	}
	if _, err := ValidateContractBodies(map[string]string{"xx": "text"}); err == nil {
		t.Error("an unknown language is refused")
	}
	if _, err := ValidateContractBodies(map[string]string{"en": "  "}); err == nil {
		t.Error("an empty text is refused")
	}
	if _, err := ValidateContractBodies(map[string]string{"en": "Dear {{customer_password}}"}); err == nil {
		t.Error("a variable that does not exist is refused: a text cannot reach other data")
	}
	if _, err := ValidateContractBodies(map[string]string{"en": strings.Repeat("x", MaxContractBody+1)}); err == nil {
		t.Error("a text past the limit is refused")
	}
	used, err := ValidateContractBodies(map[string]string{"en": "{{seller_name}}", "tr": "{{date}} {{seller_name}}"})
	if err != nil || strings.Join(used, ",") != "date,seller_name" && strings.Join(used, ",") != "seller_name,date" {
		t.Errorf("valid texts: %v %v", used, err)
	}
}

func TestContractHashIsStableAndSeesEverything(t *testing.T) {
	a := ContractHash("tos", "consumer", map[string]string{"en": "one", "tr": "iki"})
	if a != ContractHash("tos", "consumer", map[string]string{"tr": "iki", "en": "one"}) {
		t.Error("the hash depends on the order of the map")
	}
	for name, h := range map[string]string{
		"kind":     ContractHash("privacy", "consumer", map[string]string{"en": "one", "tr": "iki"}),
		"audience": ContractHash("tos", "business", map[string]string{"en": "one", "tr": "iki"}),
		"text":     ContractHash("tos", "consumer", map[string]string{"en": "one!", "tr": "iki"}),
		"language": ContractHash("tos", "consumer", map[string]string{"de": "one", "tr": "iki"}),
		// "ab"+"c" must not read as "a"+"bc": the lengths are part of the hash.
		"boundary": ContractHash("tos", "consumer", map[string]string{"en": "one\ntr\n3\niki"}),
	} {
		if h == a {
			t.Errorf("changing the %s does not change the hash", name)
		}
	}
}

func TestRenderContractHTMLEscapesEverything(t *testing.T) {
	body := "# Title <script>alert(1)</script>\n\nHello **{{customer_name}}**, you pay {{order_total}}.\nSecond line.\n\n- one\n- two <b>\n\n1. first\n2. second\n\n{{nope}} and ** unfinished"
	got := RenderContractHTML(body, map[string]string{"customer_name": `Eve <img src=x onerror=1> & **sneaky**`, "order_total": "12.00"}, false)
	for _, bad := range []string{"<script", "<img", "<b>", "onerror=1>"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q reached the page: %s", bad, got)
		}
	}
	for _, want := range []string{"<h3>Title &lt;script&gt;", "<strong>Eve &lt;img", "&amp; **sneaky**</strong>", "<li>two &lt;b&gt;</li>", "<ol>", "12.00", "{{nope}}", "Hello <strong>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// A value is never read as markdown: its ** stays text although it sits inside the bold span of the template.
	if strings.Count(got, "<strong>") != 1 {
		t.Errorf("a value made markup: %s", got)
	}
	if m := RenderContractHTML("{{seller_name}}", map[string]string{"seller_name": "Acme"}, true); !strings.Contains(m, "<mark>Acme</mark>") {
		t.Errorf("preview does not mark variables: %s", m)
	}
}

func TestChooseSeriesAndCovers(t *testing.T) {
	consumer := ContractSeries{Kind: ContractTOS, Audience: AudienceConsumer, Number: 3, MaterialNumber: 2}
	business := ContractSeries{Kind: ContractTOS, Audience: AudienceBusiness, Number: 1, MaterialNumber: 1}
	all := []ContractSeries{consumer, business, {Kind: ContractPrivacy, Audience: AudienceConsumer, Number: 1, MaterialNumber: 1}}
	if s, _ := ChooseSeries(all, ContractTOS, AudienceBusiness); s.Audience != AudienceBusiness {
		t.Error("a business customer gets the business series")
	}
	if s, _ := ChooseSeries(all, ContractPrivacy, AudienceBusiness); s.Audience != AudienceConsumer {
		t.Error("a business customer gets the consumer text when there is no business one")
	}
	if _, ok := ChooseSeries(all, ContractDistanceSales, AudienceConsumer); ok {
		t.Error("a kind nobody published has no series")
	}
	if AudienceOf("") != AudienceConsumer || AudienceOf("individual") != AudienceConsumer || AudienceOf("business") != AudienceBusiness {
		t.Error("an unknown customer type counts as a consumer")
	}
	cases := []struct {
		accepted AcceptedNumbers
		want     bool
	}{{AcceptedNumbers{Number: 3}, true}, {AcceptedNumbers{Number: 2}, true}, {AcceptedNumbers{Number: 1}, false}, {AcceptedNumbers{}, false}}
	for _, c := range cases {
		if got := c.accepted.Covers(consumer, false); got != c.want {
			t.Errorf("accepted %+v: covers = %v, want %v (a minor version 3 does not ask again, a material 2 does)", c.accepted, got, c.want)
		}
	}
	ds := ContractSeries{Kind: ContractDistanceSales, Audience: AudienceConsumer, Number: 1, MaterialNumber: 1}
	if (AcceptedNumbers{Number: 1}).Covers(ds, true) {
		t.Error("the distance sales contract accepted without the immediate-start consents does not count when the seller asks for them")
	}
	if !(AcceptedNumbers{Number: 1, ConsentNumber: 1}).Covers(ds, true) {
		t.Error("with the consents it counts")
	}
	if !(AcceptedNumbers{Number: 3}).Covers(consumer, true) {
		t.Error("the consents matter for the distance sales contract only")
	}
}

func TestPickContractBody(t *testing.T) {
	bodies := map[string]string{"tr": "turkce", "de": "deutsch"}
	if b, l := PickContractBody(bodies, "tr"); b != "turkce" || l != "tr" {
		t.Error("the requested language")
	}
	if b, l := PickContractBody(bodies, "fr"); l != "tr" || b != "turkce" {
		t.Errorf("no English, no French: the first language in the fixed order, got %s", l)
	}
	if _, l := PickContractBody(map[string]string{"en": "e", "tr": "t"}, "fr"); l != "en" {
		t.Error("English is the fallback")
	}
}

func TestSnapshotIsDeterministicAndHasNoPersonalData(t *testing.T) {
	s := Snapshot{Seller: "Acme", Customer: "Ada", Email: "ada@example.com", AcceptedAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Locale: "tr", Values: map[string]string{"seller_name": "Acme"},
		Items: []SnapshotItem{{Kind: ContractTOS, Number: 2, LocaleServed: "tr", Body: "Merhaba {{seller_name}}", BodySHA256: "aa", VersionSHA256: "bb", CheckboxText: "Okudum"}},
		ImmediateStart: "Hemen başlasın", WaiverAck: "Cayma hakkımı kaybederim", Source: "hosted", Observed: map[string]bool{"ip": true, "time": true}}
	a, b := RenderSnapshot(s), RenderSnapshot(s)
	if a != b || TextHash(a) != TextHash(b) {
		t.Fatal("the same acceptance renders two pages")
	}
	for _, want := range []string{`lang="tr"`, "Kabul kaydı", "Kullanım koşulları", "Merhaba Acme", "Okudum", "Hemen başlasın", "Cayma hakkımı kaybederim", "ip: Payrudder gözlemledi"} {
		if !strings.Contains(a, want) {
			t.Errorf("snapshot lacks %q", want)
		}
	}
	if strings.Contains(a, "<script") || strings.Contains(a, "src=") || strings.Contains(a, "http") {
		t.Error("the snapshot loads or runs something")
	}
	s.AcceptedAt = s.AcceptedAt.Add(time.Second)
	if RenderSnapshot(s) == a {
		t.Error("the time is part of the page")
	}
}

func TestStarterNoticeIsDetected(t *testing.T) {
	if !HasTemplateNotice("**"+NoticeEN+"**\n\nbody") || !HasTemplateNotice(NoticeTR) || HasTemplateNotice("a text a lawyer reviewed") {
		t.Error("template notice detection")
	}
}
