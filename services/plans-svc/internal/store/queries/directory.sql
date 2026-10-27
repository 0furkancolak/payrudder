-- The tenant directory (ADR 0018): where each tenant's data lives. Run as plans_system by internal/directory only.

-- name: GetPlacement :one
SELECT id, cell, region, placement_status FROM tenants WHERE id = $1;

-- name: LockPlacement :one
SELECT id, cell, region, placement_status FROM tenants WHERE id = $1 FOR UPDATE;

-- A move is fenced (ready -> moving), ended in a new place (moving -> ready, cell and region set) or ended where it began (moving -> ready). Each runs after
-- LockPlacement, in its transaction, and changes nothing unless the tenant is in the state the step expects.

-- name: BeginMove :execrows
UPDATE tenants SET placement_status = 'moving' WHERE id = $1 AND placement_status = 'ready';

-- name: FinishMove :execrows
UPDATE tenants SET cell = @cell::text, region = @region::text, placement_status = 'ready' WHERE id = @id AND placement_status = 'moving';

-- name: AbortMove :execrows
UPDATE tenants SET placement_status = 'ready' WHERE id = $1 AND placement_status = 'moving';

-- name: ListPlacements :many
-- Tenants in a cell, in id order; an empty @cell lists every cell.
SELECT id, cell, region, placement_status FROM tenants
WHERE cell <> '' AND (@cell::text = '' OR cell = @cell::text) AND id > @after::uuid
ORDER BY id LIMIT @page_size::int;
