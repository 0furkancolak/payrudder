package workerx

import (
	"context"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
)

type plainInbox struct{ lookups, stores int }

func (p *plainInbox) Lookup(context.Context, uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	p.lookups++
	return nil, false, nil
}

func (p *plainInbox) Store(context.Context, uuid.UUID, *commandsv1.ReplyEnvelope) error {
	p.stores++
	return nil
}

// tenantInbox answers to the tenant of the command, and must not be asked the tenant-less way.
type tenantInbox struct {
	plainInbox
	looked, stored []uuid.UUID
}

func (i *tenantInbox) LookupFor(_ context.Context, tenant, _ uuid.UUID) (*commandsv1.ReplyEnvelope, bool, error) {
	i.looked = append(i.looked, tenant)
	return nil, false, nil
}

func (i *tenantInbox) StoreFor(_ context.Context, tenant, _ uuid.UUID, _ *commandsv1.ReplyEnvelope) error {
	i.stored = append(i.stored, tenant)
	return nil
}

// A Worker asks an inbox that knows tenants for the tenant of the command, and any other inbox the way it always did.
func TestWorkerUsesTheTenantInboxWhenThereIsOne(t *testing.T) {
	ctx := context.Background()
	tenant, command := uuid.New(), uuid.New()

	plain := &plainInbox{}
	w := &Worker{Inbox: plain}
	if _, _, err := w.lookup(ctx, tenant, command); err != nil {
		t.Fatal(err)
	}
	if err := w.store(ctx, tenant, command, &commandsv1.ReplyEnvelope{}); err != nil {
		t.Fatal(err)
	}
	if plain.lookups != 1 || plain.stores != 1 {
		t.Errorf("plain inbox: %d lookups, %d stores; want one of each", plain.lookups, plain.stores)
	}

	ti := &tenantInbox{}
	w = &Worker{Inbox: ti}
	if _, _, err := w.lookup(ctx, tenant, command); err != nil {
		t.Fatal(err)
	}
	if err := w.store(ctx, tenant, command, &commandsv1.ReplyEnvelope{}); err != nil {
		t.Fatal(err)
	}
	if len(ti.looked) != 1 || ti.looked[0] != tenant || len(ti.stored) != 1 || ti.stored[0] != tenant {
		t.Errorf("tenant inbox: looked %v, stored %v; want the command's tenant once each", ti.looked, ti.stored)
	}
	if ti.lookups != 0 || ti.stores != 0 {
		t.Errorf("the tenant-less methods were used %d and %d times", ti.lookups, ti.stores)
	}
}
