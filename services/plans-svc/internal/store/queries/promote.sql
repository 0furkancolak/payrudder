-- The audit of promoting sandbox configuration to live (phase 45). Run as plans_system from internal/promote only.

-- name: InsertConfigPromotion :exec
INSERT INTO config_promotions (id, app_id, user_id, sandbox_tenant_id, live_tenant_id, created, unchanged, conflicts, skipped, failed, detail)
VALUES (@id, @app_id, @user_id, @sandbox_tenant_id, @live_tenant_id, @created, @unchanged, @conflicts, @skipped, @failed, @detail);
