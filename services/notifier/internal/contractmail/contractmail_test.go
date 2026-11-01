package contractmail

import (
	"strings"
	"testing"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestMailDataCarriesTheBodyOfThePageAndItsHash(t *testing.T) {
	page := "<!doctype html>\n<html lang=\"en\"><head><title>x</title><style>body{}</style></head><body>\n<h1>Record</h1>\n<p>text</p>\n</body></html>\n"
	d := MailData(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC), "Ada", "Acme", "Europe/Istanbul", "tr", &plansv1.GetContractSnapshotResponse{Html: page, Sha256: "h"})
	if d["snapshot_html"] != "<h1>Record</h1>\n<p>text</p>" || d["snapshot_sha256"] != "h" || d["locale"] != "tr" || !strings.HasPrefix(d["accepted_at"], "2 Nov 2026, 10:30") {
		t.Errorf("mail data = %v", d)
	}
	if bad := MailData(time.Now(), "A", "B", "Nowhere/Land", "", &plansv1.GetContractSnapshotResponse{Html: "no body tags"}); bad["snapshot_html"] != "no body tags" || !strings.Contains(bad["accepted_at"], "UTC") {
		t.Errorf("a zone this host does not know is UTC, a page without body tags is printed whole: %v", bad)
	}
}
