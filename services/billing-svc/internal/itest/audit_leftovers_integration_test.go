//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// auditEntries reads the entries of one action for the tenant, oldest first: the target and the decoded detail of each (plan 22 G4: the hooks of the settings and
// payments an operator or a seller changes).
func auditEntries(t *testing.T, tid uuid.UUID, action string) (targets []string, details []map[string]any) {
	t.Helper()
	rows, err := shared.sql.Query(context.Background(), `SELECT target_id, detail::text FROM audit_events WHERE tenant_id = $1 AND action = $2 ORDER BY created_at, id`, tid, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var target, raw string
		if err := rows.Scan(&target, &raw); err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		targets, details = append(targets, target), append(details, d)
	}
	return targets, details
}
