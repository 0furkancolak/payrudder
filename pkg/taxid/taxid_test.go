package taxid

import "testing"

// The vectors of the two Turkish checksums were produced and judged by python-stdnum (stdnum.tr.vkn, stdnum.tr.tckimlik), an implementation that is not this one:
// valid numbers, numbers with one digit changed, random ones, and the shapes that are wrong before any checksum.
var vknVectors = []struct {
	id string
	ok bool
}{
	{"3667127684", true},
	{"2233079244", true},
	{"1177774121", true},
	{"8072902227", true},
	{"7706589481", true},
	{"4504000883", true},
	{"4499207074", true},
	{"8341704718", true},
	{"5440910317", true},
	{"5164388317", true},
	{"5260181590", false},
	{"8301661318", false},
	{"6091390996", false},
	{"0308246281", false},
	{"9482199351", false},
	{"8190937865", false},
	{"7975432319", false},
	{"4875749118", false},
	{"6252760189", false},
	{"5559797114", false},
	{"7104974650", false},
	{"7529170342", false},
	{"2684656321", false},
	{"0268599528", false},
	{"5667127684", false},
	{"0233079244", false},
	{"1175774121", false},
	{"8072912227", false},
	{"7706989481", false},
	{"4604000883", false},
	{"", false},
	{"123456789", false},
	{"12345678901", false},
	{"123456789a", false},
}
var tcknVectors = []struct {
	id string
	ok bool
}{
	{"11995305520", true},
	{"92812646446", true},
	{"36670009494", true},
	{"13453709710", true},
	{"95986365168", true},
	{"29072637792", true},
	{"95113115444", true},
	{"10758669428", true},
	{"40394525642", true},
	{"34578364820", true},
	{"49931582794", false},
	{"44093056529", false},
	{"51307871616", false},
	{"14956605366", false},
	{"40626116957", false},
	{"32008261995", false},
	{"92254282116", false},
	{"83420750961", false},
	{"33969372930", false},
	{"78265123308", false},
	{"15169784649", false},
	{"46657872009", false},
	{"87379727611", false},
	{"35651788002", false},
	{"11595305520", false},
	{"92812646456", false},
	{"36640009494", false},
	{"13453700710", false},
	{"95986565168", false},
	{"29372637792", false},
	{"00000000000", false},
	{"1234567890", false},
	{"123456789012", false},
	{"1000000014a", false},
}

func TestTurkishChecksumsAgreeWithAnIndependentImplementation(t *testing.T) {
	for _, c := range vknVectors {
		if got := ValidVKN(c.id); got != c.ok {
			t.Errorf("ValidVKN(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
	for _, c := range tcknVectors {
		if got := ValidTCKN(c.id); got != c.ok {
			t.Errorf("ValidTCKN(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
}

// The vectors of the VAT checksums were produced and judged by python-stdnum (stdnum.eu.vat, which routes to the module of each country), the same implementation as
// above: the valid ones are the examples of its documentation or numbers found by searching and judged valid by it, the invalid ones are those of its documentation or a
// valid one with one character changed that it judges invalid. Every one of them has the shape of a VAT id, so a false is the checksum's doing. They are the vectors of
// euVatVectors in web/admin/src/shared/lib/__tests__/taxid.test.ts. A country with no checksum here (SK, XI) has no vector; see euVATWithoutChecksum.
var euVATVectors = []struct {
	id string
	ok bool
}{
	{"ATU13585627", true},
	{"ATU91621601", true},
	{"ATU13585628", false},
	{"ATU13585637", false},
	{"ATU91621602", false},
	{"BE0428759497", true},
	{"BE0403019261", true},
	{"BE0428759498", false},
	{"BE0428759407", false},
	{"BE0403019262", false},
	{"BG175074752", true},
	{"BG8973928695", true},
	{"BG175074753", false},
	{"BG175074762", false},
	{"CY10259033P", true},
	{"CY95388011J", true},
	{"CY10259033Q", false},
	{"CY10259034P", false},
	{"CY95388011K", false},
	{"CZ25123891", true},
	{"CZ640903926", true},
	{"CZ25123892", false},
	{"CZ25123801", false},
	{"CZ640903927", false},
	{"DE136695976", true},
	{"DE188007596", true},
	{"DE136695978", false},
	{"DE136695977", false},
	{"DE136695986", false},
	{"DE188007597", false},
	{"DK13585628", true},
	{"DK16913898", true},
	{"DK13585629", false},
	{"DK13585638", false},
	{"DK16913899", false},
	{"EE100594102", true},
	{"EE100931558", true},
	{"EE100594103", false},
	{"EE100594112", false},
	{"EE100931559", false},
	{"EL094259216", true},
	{"EL498861492", true},
	{"EL123456781", false},
	{"EL094259217", false},
	{"EL094259226", false},
	{"EL498861493", false},
	{"ESB64717838", true},
	{"ES54362315K", true},
	{"ESX5253868R", true},
	{"ESM1234567L", true},
	{"ESB64717839", false},
	{"ESB64717848", false},
	{"ES54362315L", false},
	{"ESX5253868S", false},
	{"ESM1234567M", false},
	{"FI20774740", true},
	{"FI45170724", true},
	{"FI20774741", false},
	{"FI20774750", false},
	{"FI45170725", false},
	{"FR23334175221", true},
	{"FR40303265045", true},
	{"FRK7399859412", true},
	{"FR4Z123456782", true},
	{"FR84323140391", false},
	{"FRIO334175221", false},
	{"FR23334175222", false},
	{"FR23334175231", false},
	{"FR40303265046", false},
	{"FRK7399859413", false},
	{"HR33392005961", true},
	{"HR58626137648", true},
	{"HR33392005962", false},
	{"HR33392005971", false},
	{"HR58626137649", false},
	{"HU12892312", true},
	{"HU04412427", true},
	{"HU12892313", false},
	{"HU12892322", false},
	{"HU04412428", false},
	{"IE6433435F", true},
	{"IE6433435OA", true},
	{"IE8D79739I", true},
	{"IE6433435G", false},
	{"IE6433436F", false},
	{"IE6433435OB", false},
	{"IE8D79739J", false},
	{"IT00743110157", true},
	{"IT59576010843", true},
	{"IT00743110158", false},
	{"IT00743110167", false},
	{"IT59576010844", false},
	{"LT119511515", true},
	{"LT100001919017", true},
	{"LT100004801610", true},
	{"LT119511516", false},
	{"LT119511525", false},
	{"LT100001919018", false},
	{"LT100004801611", false},
	{"LU15027442", true},
	{"LU82289501", true},
	{"LU15027443", false},
	{"LU15027452", false},
	{"LU82289502", false},
	{"LV40003521600", true},
	{"LV22069520547", true},
	{"LV40003521601", false},
	{"LV40003521610", false},
	{"MT11679112", true},
	{"MT60282319", true},
	{"MT11679113", false},
	{"MT11679122", false},
	{"MT60282310", false},
	{"NL004495445B01", true},
	{"NL002455799B11", true},
	{"NL123456789B90", false},
	{"NL004495545B01", false},
	{"NL002455899B11", false},
	{"PL8567346215", true},
	{"PL6161379700", true},
	{"PL8567346216", false},
	{"PL8567346225", false},
	{"PL6161379701", false},
	{"PT501964843", true},
	{"PT204433568", true},
	{"PT501964844", false},
	{"PT501964853", false},
	{"PT204433569", false},
	{"RO18547290", true},
	{"RO70138", true},
	{"RO18547291", false},
	{"RO18547200", false},
	{"RO70139", false},
	{"SE123456789701", true},
	{"SE927293143801", true},
	{"SE123456789101", false},
	{"SE123456789801", false},
	{"SE123456889701", false},
	{"SE927293143901", false},
	{"SI50223054", true},
	{"SI96862335", true},
	{"SI50223055", false},
	{"SI50223064", false},
	{"SI96862336", false},
}

// Numbers python-stdnum judges invalid (the second of each pair) that this package accepts, because the branch they belong to is shape-only: the two countries without an
// algorithm here (SK, XI) and the numbers made of a person's birth date (CZ with 9 or 10 digits, BG with 10, the personal codes of LV). A warning on a good number is worse
// than none on a bad one, so what is not verified is not guessed at; the first of each pair is judged valid by stdnum.
var euVATWithoutChecksum = []string{"SK2022749619", "SK2022749610", "XI123456782", "XI123456783", "CZ7103192745", "CZ7103192746", "BG7523169263", "BG7523169264", "LV16117519997", "LV16137519997"}

func TestEUVATChecksumsAgreeWithAnIndependentImplementation(t *testing.T) {
	for _, c := range euVATVectors {
		if !ValidEUVATShape(c.id) {
			t.Errorf("%q must have the shape of a VAT id: a vector tests the checksum", c.id)
		}
		if got := ValidEUVAT(c.id); got != c.ok {
			t.Errorf("ValidEUVAT(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
	for _, id := range euVATWithoutChecksum {
		if !ValidEUVAT(id) {
			t.Errorf("%q has no checksum here and the shape of a VAT id, so it is accepted", id)
		}
	}
	if !ValidEUVAT("de 136.695-976") || ValidEUVAT("DE 136 695 978") || ValidEUVAT("DE12345") || ValidEUVAT("US136695976") {
		t.Error("spaces and dots do not matter, and a wrong shape or checksum is refused")
	}
}

func TestNormalizeKeepsWhatPeopleTypeForReadability(t *testing.T) {
	for in, want := range map[string]string{"de 136.695-976": "DE136695976", " 10000000146 ": "10000000146", "tr": "TR"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if !ValidTCKN("100 000 001 46") || !ValidVKN("123-456-7890") {
		t.Error("spaces and dashes typed for readability must not make a good number bad")
	}
}

func TestEUVATShape(t *testing.T) {
	for _, id := range []string{"DE136695976", "FR40303265045", "ESX1234567L", "NL004495445B01", "ATU12345675", "IT00743110157", "BE0123456749", "PL5260250995", "SE556016068101", "DK13585628", "FI20774740",
		"IE6388047V", "PT501964843", "CZ25123891", "HU12892312", "RO18547290", "EL094259216", "LU15027442", "BG175074752", "HR33392005961", "CY10259033P", "EE100931558", "LV40003521600",
		"LT119511515", "MT11649925", "SI50223054", "SK2022749619", "XI123456782", "de136695976", "DE 136 695 976"} {
		if !ValidEUVATShape(id) {
			t.Errorf("%q has the shape of a VAT id", id)
		}
	}
	for _, id := range []string{"DE12345", "FR1", "ESXX", "NL12345678B01", "ATU1234", "US123456789", "", "DE13669597a", "DE1366959761", "SE55601606810", "BE2123456749"} {
		if ValidEUVATShape(id) {
			t.Errorf("%q has not the shape of a VAT id", id)
		}
	}
}

func TestDetectAndValid(t *testing.T) {
	for id, want := range map[string]Kind{"10000000146": TCKN, "1234567890": VKN, "DE136695976": EUVAT} {
		if got, ok := Detect(id); !ok || got != want {
			t.Errorf("Detect(%q) = %q %v, want %q", id, got, ok, want)
		}
		if !Valid(want, id) {
			t.Errorf("Valid(%q, %q) = false", want, id)
		}
	}
	if _, ok := Detect("12345678901"); ok {
		t.Error("a number that passes no checksum is not guessed at")
	}
	if _, ok := Detect("DE136695978"); ok || Valid(EUVAT, "DE136695978") {
		t.Error("a VAT id with a wrong check digit is not recognised")
	}
	if Valid("passport", "X") || Valid(TCKN, "1234567890") {
		t.Error("an unknown kind is never valid, and a VKN is not a TCKN")
	}
}

// The same cases, with the same keys, as taxIdHint in web/admin/src/shared/lib/__tests__/taxid.test.ts: the server's warning and the form's hint must not disagree.
func TestCheckIsAHintNeverARefusal(t *testing.T) {
	for _, c := range []struct {
		id, country, kind string
		want              Hint
	}{
		{"", "TR", "business", HintNone},
		{"1234567890", "TR", "business", HintNone},
		{"10000000146", "TR", "individual", HintNone},
		{"anything", "US", "business", HintNone},
		{"anything", "DE", "individual", HintNone},
		{"whatever", "", "", HintNone},
		{"10000000146", "TR", "business", HintTRIsTCKN},
		{"1234567890", "TR", "individual", HintTRIsVKN},
		{"12345", "TR", "business", HintTRBusiness},
		{"12345", "TR", "individual", HintTRIndividual},
		{"12345", "TR", "", HintTRAny},
		{"1234567890", "TR", "", HintNone},
		{"10000000146", "tr", "", HintNone},
		{"DE136695976", "DE", "business", HintNone},
		{"DE 136 695 976", "de", "business", HintNone},
		{"DE12", "DE", "business", HintEUVAT},
		{"GR", "GR", "business", HintEUVAT},
		{"DE136695978", "DE", "business", HintEUVAT},
		{"FR84323140391", "FR", "business", HintEUVAT},
		{"EL094259216", "GR", "business", HintNone},
		{"SK2022749610", "SK", "business", HintNone},
		{"DE136695978", "DE", "individual", HintNone},
	} {
		if got := Check(c.id, c.country, c.kind); got != c.want {
			t.Errorf("Check(%q, %q, %q) = %q, want %q", c.id, c.country, c.kind, got, c.want)
		}
	}
}

func TestRecognised(t *testing.T) {
	for id, want := range map[string]bool{"10000000146": true, "1234567890": true, "DE136695976": true, "DE136695978": false, "12345": false, "": false, "12345678901": false} {
		if got := Recognised(id); got != want {
			t.Errorf("Recognised(%q) = %v, want %v", id, got, want)
		}
	}
}
