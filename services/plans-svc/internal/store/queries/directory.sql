-- The tenant directory (ADR 0018): where each tenant's data lives. Run as plans_system by internal/directory only.

-- name: GetPlacement :one
SELECT id, cell, region, placement_status FROM tenants WHERE id = $1;

-- name: ListPlacements :many
-- Tenants in a cell, in id order; an empty @cell lists every cell.
SELECT id, cell, region, placement_status FROM tenants
WHERE cell <> '' AND (@cell::text = '' OR cell = @cell::text) AND id > @after::uuid
ORDER BY id LIMIT @page_size::int;
