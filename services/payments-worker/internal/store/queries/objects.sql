-- name: GetProviderObjectRef :one
SELECT ref FROM provider_objects WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND kind = @kind AND key = @key;

-- name: PutProviderObject :exec
INSERT INTO provider_objects (tenant_id, provider, environment, kind, key, ref)
VALUES (@tenant_id, @provider, @environment, @kind, @key, @ref) ON CONFLICT DO NOTHING;

-- name: GetProviderObjectKey :one
SELECT key FROM provider_objects
WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND kind = @kind AND ref = @ref LIMIT 1;

-- name: ListProviderObjects :many
SELECT kind, key, ref FROM provider_objects
WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND kind = ANY(@kinds::text[]) ORDER BY kind, key;
