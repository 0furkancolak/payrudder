-- A voided invoice gives back the credit balances it consumed: its applications are deleted and the lots are open again.
-- +goose Up
GRANT DELETE ON credit_applications TO billing_system;

-- +goose Down
REVOKE DELETE ON credit_applications FROM billing_system;
