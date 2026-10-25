package handler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/workerx"
)

// countStore fails the test's expectations by counting what reached storage.
type countStore struct{ heads, puts atomic.Int32 }

func (s *countStore) Head(context.Context, string) (blob.Info, error) {
	s.heads.Add(1)
	return blob.Info{}, blob.ErrNotFound
}
func (s *countStore) Get(context.Context, string) ([]byte, error) { return nil, blob.ErrNotFound }
func (s *countStore) Put(context.Context, string, []byte, blob.PutOptions) error {
	s.puts.Add(1)
	return nil
}

func renderCmd(environment plansv1.Environment, number string) *commandsv1.CommandEnvelope {
	return &commandsv1.CommandEnvelope{TenantId: "t", Environment: environment, Command: &commandsv1.CommandEnvelope_RenderInvoicePdf{
		RenderInvoicePdf: &commandsv1.RenderInvoicePdf{Invoice: &commandsv1.InvoiceSnapshot{InvoiceId: "i", Version: 1, Number: number, Currency: "USD", CurrencyExponent: 2,
			Lines: []*commandsv1.InvoiceLineSnapshot{{LineNo: 1, Kind: "BASE_FEE", Description: "Base fee", AmountMinor: 1000}}, SubtotalMinor: 1000, TotalMinor: 1000}}}}
}

// The watermark and the bucket tag follow the environment, so the worker never renders a command that does not name one, or one whose invoice number is of the
// other series: that would print a real invoice as a sandbox one, or a sandbox one as real. Such a command is dead-lettered and touches no storage.
func TestRenderRefusesWhatDisagreesAboutTheEnvironment(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd  *commandsv1.CommandEnvelope
		want error
	}{
		"no environment":            {renderCmd(plansv1.Environment_ENVIRONMENT_UNSPECIFIED, "INV-2026-000001"), env.ErrMissing},
		"sandbox command, live no.": {renderCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, "INV-2026-000001"), env.ErrMismatch},
		"live command, sandbox no.": {renderCmd(plansv1.Environment_ENVIRONMENT_LIVE, "SBX-2026-000001"), env.ErrMismatch},
	} {
		st := &countStore{}
		_, err := (&Handler{Store: st}).Handle(context.Background(), tc.cmd)
		if !errors.Is(err, workerx.ErrPoison) || !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want poison wrapping %v", name, err, tc.want)
		}
		if st.heads.Load() != 0 || st.puts.Load() != 0 {
			t.Errorf("%s: storage was touched (%d heads, %d puts)", name, st.heads.Load(), st.puts.Load())
		}
	}
	// The agreeing ones render, and the tag says which environment they are.
	for _, tc := range []struct {
		cmd *commandsv1.CommandEnvelope
		tag string
	}{
		{renderCmd(plansv1.Environment_ENVIRONMENT_SANDBOX, "SBX-2026-000001"), "sandbox"},
		{renderCmd(plansv1.Environment_ENVIRONMENT_LIVE, "INV-2026-000001"), "live"},
	} {
		var tagged string
		st := &tagStore{tag: &tagged}
		if _, err := (&Handler{Store: st}).Handle(context.Background(), tc.cmd); err != nil {
			t.Fatalf("%s: %v", tc.tag, err)
		}
		if tagged != tc.tag {
			t.Errorf("tag = %q, want %q", tagged, tc.tag)
		}
	}
}

type tagStore struct{ tag *string }

func (s *tagStore) Head(context.Context, string) (blob.Info, error) {
	return blob.Info{}, blob.ErrNotFound
}
func (s *tagStore) Get(context.Context, string) ([]byte, error) { return nil, blob.ErrNotFound }
func (s *tagStore) Put(_ context.Context, _ string, _ []byte, o blob.PutOptions) error {
	*s.tag = o.Tags["environment"]
	return nil
}
