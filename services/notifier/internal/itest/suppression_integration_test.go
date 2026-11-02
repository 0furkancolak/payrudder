//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/mailx/fakenafru"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/mailevents"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/migrations"
)

// A mail goes out through a fake Nafru, the provider reports a permanent bounce at the notifier's endpoint, and the next mail to that address is not sent, while
// another address and another tenant are not affected; the operator lifts the suppression and the address is mailed again. Real Postgres, a fake provider: what
// Nafru's own bounce looks like is UNVERIFIED (see mailx.Nafru).
func TestBounceSuppressesTheNextMail(t *testing.T) {
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "notifier", migrations.FS)
	pool, err := pgxx.NewPool(context.Background(), db.System)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	nafru := fakenafru.New()
	defer nafru.Close()
	notes := store.Notifications{DB: pool}
	list := store.Suppressions{DB: pool}
	mailer := &delivery.Mailer{Default: mailx.Nafru{Resend: mailx.Resend{BaseURL: nafru.URL, APIKey: "nk"}}, From: "Payrudder <billing@payrudder.test>", Notes: notes, Suppress: list}
	events := httptest.NewServer((&mailevents.Handler{Secret: "whsec_integration_test", Notes: notes, List: list, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Routes())
	defer events.Close()

	ctx := context.Background()
	tenantA, tenantB := uuid.New(), uuid.New()
	send := func(tenant uuid.UUID, key, to string) string {
		t.Helper()
		id, err := mailer.Deliver(ctx, delivery.Delivery{Key: key, Kind: "command", Template: "payment_failed.v1", TenantID: tenant, To: to, Data: map[string]string{"customer_name": "Ada"}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	first := send(tenantA, "k1", "Ada@Example.com")
	if first == delivery.Dropped || len(nafru.Sent()) != 1 || nafru.Sent()[0].Tags["payrudder_template"] != "payment_failed_v1" {
		t.Fatalf("the first mail: id %q, sent %+v", first, nafru.Sent())
	}
	tid, recipient, found, err := notes.ByMessageID(ctx, first)
	if err != nil || !found || tid != tenantA || recipient != "Ada@Example.com" {
		t.Fatalf("the provider id leads back to the tenant and the address: %v %q %v %v", tid, recipient, found, err)
	}

	status, err := fakenafru.Emit(ctx, events.URL, "whsec_integration_test", mailx.Event{Type: mailx.EventBounced, MessageID: first, BounceType: mailx.BounceHard, Detail: "550 no such user"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("bounce event: %d %v", status, err)
	}
	if on, _ := list.IsSuppressed(ctx, tenantA, "ada@example.com"); !on {
		t.Fatal("a permanent bounce must put the address on the list, whatever its case")
	}

	// The message log says the first mail bounced; nobody else's does.
	msgs, err := notes.ListForTenant(ctx, tenantA, store.MessagesAfter{}, 10)
	if err != nil || len(msgs) != 1 || msgs[0].Key != "k1" || msgs[0].State != "bounced" || msgs[0].Recipient != "Ada@Example.com" || msgs[0].Template != "payment_failed.v1" || msgs[0].SendTime == nil {
		t.Fatalf("the tenant's message log after the bounce = %+v, %v; want the one mail, bounced", msgs, err)
	}

	if id := send(tenantA, "k2", "ada@example.com"); id != delivery.Dropped || len(nafru.Sent()) != 1 {
		t.Errorf("the next mail to a bounced address: id %q, %d sent", id, len(nafru.Sent()))
	}
	if id := send(tenantA, "k3", "bob@example.com"); id == delivery.Dropped || len(nafru.Sent()) != 2 {
		t.Errorf("another address is mailed: %q, %d sent", id, len(nafru.Sent()))
	}
	if id := send(tenantB, "k4", "ada@example.com"); id == delivery.Dropped || len(nafru.Sent()) != 3 {
		t.Errorf("another tenant's mail to the same address is not suppressed: %q, %d sent", id, len(nafru.Sent()))
	}

	// The operator lifts it, through the signed endpoint, and the address is mailed again (the dropped mail released its claim, so its key is free).
	req, _ := http.NewRequest(http.MethodDelete, events.URL+"/v1/mail-suppressions/"+tenantA.String()+"/ada@example.com", nil)
	req.Header.Set(webhooksig.Header(), webhooksig.SignRequest(time.Now(), http.MethodDelete, req.URL, nil, "whsec_integration_test"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("lift: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	if id := send(tenantA, "k2", "ada@example.com"); id == delivery.Dropped || len(nafru.Sent()) != 4 {
		t.Errorf("after the suppression is lifted: %q, %d sent", id, len(nafru.Sent()))
	}
	// The log of tenant A: newest first, three mails (the dropped one left no row), the first still bounced, and a page of two continues with the third.
	page1, err := notes.ListForTenant(ctx, tenantA, store.MessagesAfter{}, 2)
	if err != nil || len(page1) != 2 || page1[0].Key != "k2" || page1[1].Key != "k3" || page1[0].State != "sent" {
		t.Fatalf("first page = %+v, %v", page1, err)
	}
	page2, err := notes.ListForTenant(ctx, tenantA, store.MessagesAfter{At: page1[1].ClaimTime, Key: page1[1].Key}, 2)
	if err != nil || len(page2) != 1 || page2[0].Key != "k1" || page2[0].State != "bounced" {
		t.Fatalf("second page = %+v, %v", page2, err)
	}
	if other, _ := notes.ListForTenant(ctx, uuid.New(), store.MessagesAfter{}, 10); len(other) != 0 {
		t.Errorf("a tenant with no mail sees %+v", other)
	}
}
