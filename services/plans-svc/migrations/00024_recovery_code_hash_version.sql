-- Security review: recovery codes were hashed with HMAC keyed by the pepper itself, the key that also hashes the six-digit sign-in codes. New codes are hashed with a key derived
-- from the pepper for recovery codes alone (version 2); codes made before this keep their hash and are checked with the old derivation until they are used or replaced
-- (a new set of recovery codes, or turning two-factor sign-in off and on, makes version 2 rows). Nothing is rehashed: the code itself is not stored.
-- People, not tenants: the pooled database only, no tenant twin.

-- +goose Up
ALTER TABLE user_recovery_codes ADD COLUMN hash_version smallint NOT NULL DEFAULT 1 CHECK (hash_version IN (1, 2));

-- +goose Down
ALTER TABLE user_recovery_codes DROP COLUMN hash_version;
