// Package query reads customers.
package query

import (
	"context"
	"encoding/base64"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads customers.
type Queries struct{ db *store.DB }

// New returns the customer queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Page tokens are opaque to clients: base64url of the last external_id returned.
func encodePageToken(lastExternalID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(lastExternalID))
}

func decodePageToken(tok string) (string, error) {
	if tok == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	return string(b), err
}

// ByExternalID returns the customer the tenant knows by its own id.
func (r *Queries) ByExternalID(ctx context.Context, externalID string) (*plansv1.Customer, error) {
	if externalID == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	var out sqlc.Customer
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.GetCustomerByExternalID(ctx, externalID)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// ByID returns the customer with the given Quotify id.
func (r *Queries) ByID(ctx context.Context, id string) (*plansv1.Customer, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, domain.Invalid("id", "must be a UUID")
	}
	var out sqlc.Customer
	err = r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.GetCustomerByID(ctx, uid)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// List returns a page of customers in external id order.
func (r *Queries) List(ctx context.Context, req *plansv1.ListCustomersRequest) (*plansv1.ListCustomersResponse, error) {
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 100
	case size < 0 || size > 500:
		return nil, domain.Invalid("page_size", "between 1 and 500")
	}
	after, err := decodePageToken(req.GetPageToken())
	if err != nil {
		return nil, domain.Invalid("page_token", "malformed")
	}
	var rows []sqlc.Customer
	err = r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListCustomers(ctx, sqlc.ListCustomersParams{After: after, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	resp := &plansv1.ListCustomersResponse{}
	if len(rows) > int(size) {
		rows = rows[:size]
		resp.NextPageToken = encodePageToken(rows[len(rows)-1].ExternalID)
	}
	for _, c := range rows {
		resp.Customers = append(resp.Customers, convert.Customer(c))
	}
	return resp, nil
}
