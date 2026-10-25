// Package sandbox resets a sandbox (phase 45 E): a durable job that deletes a sandbox tenant's customer-side data in plans-svc and, one service after another, in
// ingest-svc, aggregator, analytics-svc, billing-svc and payments-worker, and keeps its configuration. A sandbox that nobody has touched for the retention period is
// reset the same way. plans-svc owns the job because it owns the tenant: it knows what is a sandbox, and an environment other than the sandbox is refused by the
// database (a trigger on sandbox_resets) and again by every service that is asked.
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Step is one service's part of a reset. A step without a Run is not configured on this deployment (its service has no address) and is skipped, not failed.
type Step struct {
	Name string
	Run  func(ctx context.Context, tenantID uuid.UUID) (deleted map[string]int64, err error)
}

// StepTimeout bounds one service's part.
const StepTimeout = 2 * time.Minute

// GRPC is the step of a service that implements SandboxResetService. The call is an operator call, as every call plans-svc makes to another service about a tenant.
func GRPC(name string, c adminv1.SandboxResetServiceClient) Step {
	if c == nil {
		return Step{Name: name}
	}
	return Step{Name: name, Run: func(ctx context.Context, id uuid.UUID) (map[string]int64, error) {
		ctx, cancel := context.WithTimeout(tenant.OutgoingContext(tenant.WithOperator(ctx)), StepTimeout)
		defer cancel()
		resp, err := c.ResetTenant(ctx, &adminv1.ResetTenantRequest{TenantId: id.String(), Environment: env.Sandbox.Proto()})
		return resp.GetDeleted(), err
	}}
}

// HTTP is the step of payments-worker, which has no gRPC server: POST {base}/admin/tenants/{id}/sandbox-reset with the internal token. A nil base is not configured.
func HTTP(name string, base *url.URL, token string, client *http.Client) Step {
	if base == nil {
		return Step{Name: name}
	}
	if client == nil {
		client = &http.Client{Timeout: StepTimeout}
	}
	return Step{Name: name, Run: func(ctx context.Context, id uuid.UUID) (map[string]int64, error) {
		u := *base
		u.Path = "/admin/tenants/" + id.String() + "/sandbox-reset"
		body, _ := json.Marshal(map[string]string{"environment": env.Sandbox.String()})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Internal-Token", token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("%s answered %d: %s", name, resp.StatusCode, bytes.TrimSpace(raw))
		}
		var out struct {
			Deleted map[string]int64 `json:"deleted"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("%s answered garbage: %w", name, err)
		}
		return out.Deleted, nil
	}}
}

// Names lists the names of the steps, in order.
func Names(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

// stepState is one step in the job row.
type stepState struct {
	Name    string           `json:"name"`
	State   string           `json:"state"` // pending, done, failed, skipped
	Deleted map[string]int64 `json:"deleted,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// Initial is the steps column of a new job: every step pending.
func Initial(names []string) []byte {
	states := make([]stepState, len(names))
	for i, n := range names {
		states[i] = stepState{Name: n, State: "pending"}
	}
	b, _ := json.Marshal(states)
	return b
}
