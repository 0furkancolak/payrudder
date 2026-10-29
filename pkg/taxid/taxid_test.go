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
	if Valid("passport", "X") || Valid(TCKN, "1234567890") {
		t.Error("an unknown kind is never valid, and a VKN is not a TCKN")
	}
}
