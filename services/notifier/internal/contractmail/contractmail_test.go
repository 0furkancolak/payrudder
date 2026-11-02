package contractmail

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

func TestMailDataCarriesTheBodyOfThePageAndItsHash(t *testing.T) {
	page := "<!doctype html>\n<html lang=\"en\"><head><title>x</title><style>body{}</style></head><body>\n<h1>Record</h1>\n<p>text</p>\n</body></html>\n"
	d := MailData(time.Date(2026, 11, 2, 7, 30, 0, 0, time.UTC), "Ada", "Acme", "Europe/Istanbul", "tr", &plansv1.GetContractSnapshotResponse{Html: page, Sha256: "h"})
	if d["snapshot_html"] != "<h1>Record</h1>\n<p>text</p>" || d["snapshot_sha256"] != "h" || d["locale"] != "tr" || !strings.HasPrefix(d["accepted_at"], "2 Kasım 2026, 10:30") {
		t.Errorf("mail data = %v", d)
	}
	if bad := MailData(time.Now(), "A", "B", "Nowhere/Land", "", &plansv1.GetContractSnapshotResponse{Html: "no body tags"}); bad["snapshot_html"] != "no body tags" || !strings.Contains(bad["accepted_at"], "UTC") {
		t.Errorf("a zone this host does not know is UTC, a page without body tags is printed whole: %v", bad)
	}
}

// --- the copy of an accepted agreement carries its PDF ---

type fakeContractSvc struct {
	reqs []*plansv1.GetContractSnapshotRequest
	pdf  []byte
}

func (f *fakeContractSvc) GetContractSnapshot(_ context.Context, in *plansv1.GetContractSnapshotRequest, _ ...grpc.CallOption) (*plansv1.GetContractSnapshotResponse, error) {
	f.reqs = append(f.reqs, in)
	r := &plansv1.GetContractSnapshotResponse{Html: "<html><body><h1>Record</h1></body></html>", Sha256: strings.Repeat("ab", 32)}
	if in.GetIncludePdf() {
		r.Pdf, r.PdfUnavailable = f.pdf, f.pdf == nil
	}
	return r, nil
}

type fakeCustomers struct{}

func (fakeCustomers) GetCustomer(context.Context, *plansv1.GetCustomerRequest, ...grpc.CallOption) (*plansv1.GetCustomerResponse, error) {
	return &plansv1.GetCustomerResponse{Customer: &plansv1.Customer{Name: "Ada", Email: "ada@example.com", Locale: "en"}}, nil
}

type fakeTenants struct{}

func (fakeTenants) GetTenant(context.Context, *plansv1.GetTenantRequest, ...grpc.CallOption) (*plansv1.GetTenantResponse, error) {
	return &plansv1.GetTenantResponse{Tenant: &plansv1.Tenant{Name: "Acme", Timezone: "UTC", Environment: plansv1.Environment_ENVIRONMENT_SANDBOX}}, nil
}

type notes struct{ sent map[string]string }

func (n *notes) Claim(_ context.Context, key string, _ *uuid.UUID, _ uuid.UUID, _, _, _ string) (store.Claim, string, error) {
	if id, ok := n.sent[key]; ok {
		return store.AlreadySent, id, nil
	}
	return store.Claimed, "", nil
}
func (n *notes) MarkSent(_ context.Context, key, id string) error { n.sent[key] = id; return nil }
func (n *notes) Release(context.Context, string) error            { return nil }

type sink struct{ got []mailx.Message }

func (s *sink) Send(_ context.Context, m mailx.Message) (string, error) {
	s.got = append(s.got, m)
	return "mid-1", nil
}

func acceptedEvent(t *testing.T) *kgo.Record {
	t.Helper()
	b, err := proto.Marshal(&eventsv1.ContractAccepted{EventId: "ev-1", TenantId: uuid.NewString(), CustomerId: uuid.NewString(), AcceptanceId: "acc-1", SnapshotSha256: strings.Repeat("ab", 32),
		AcceptedAt: timestamppb.New(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	return &kgo.Record{Value: b}
}

func TestTheCopyOfAnAcceptedAgreementCarriesItsPdfWhenThereIsOne(t *testing.T) {
	for name, tc := range map[string]struct {
		pdf     []byte
		attach  bool
		mention string
	}{"a text that can be drawn": {pdf: []byte("%PDF-1.3 copy"), attach: true, mention: "A PDF copy of this page is attached."}, "a text the PDF font cannot draw": {}} {
		t.Run(name, func(t *testing.T) {
			svc, out := &fakeContractSvc{pdf: tc.pdf}, &sink{}
			c := &Consumer{Mailer: &delivery.Mailer{Default: out, Sandbox: out, Notes: &notes{sent: map[string]string{}}, From: "Acme <billing@acme.test>"}, Plans: fakeCustomers{}, Contracts: svc, Admin: fakeTenants{},
				Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if err := c.one(context.Background(), acceptedEvent(t)); err != nil {
				t.Fatal(err)
			}
			if len(svc.reqs) != 1 || !svc.reqs[0].GetIncludePdf() {
				t.Errorf("the PDF was not asked of the record: %+v", svc.reqs)
			}
			if len(out.got) != 1 {
				t.Fatalf("%d mails", len(out.got))
			}
			m := out.got[0]
			if tc.attach {
				if len(m.Attachments) != 1 || m.Attachments[0].ContentType != "application/pdf" || m.Attachments[0].Filename != "agreement-"+strings.Repeat("ab", 6)+".pdf" || !bytes.Equal(m.Attachments[0].Data, tc.pdf) {
					t.Errorf("attachments: %+v", m.Attachments)
				}
			} else if len(m.Attachments) != 0 {
				t.Errorf("a mail with no PDF carries %d files", len(m.Attachments))
			}
			if has := strings.Contains(m.HTML, "A PDF copy of this page is attached."); has != tc.attach {
				t.Errorf("the mail says a PDF is attached: %v, want %v\n%s", has, tc.attach, m.HTML)
			}
			if !strings.Contains(m.HTML, "<h1>Record</h1>") {
				t.Error("the page itself is no longer in the body")
			}
		})
	}
}
