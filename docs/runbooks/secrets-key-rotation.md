# Replacing the secrets key

`PAYRUDDER_SECRETS_KEY` seals what the platform must be able to read back: tenants' provider and e-invoice credentials (payments-worker), webhook signing secrets and mail-integration keys (plans-svc). Until 2026-11 it was one key forever; a leak meant stopping the world. Now a sealed box carries the **id of its key** in its first byte (the byte was always `1` before, so every existing box is a box of key 1 and opens as before), and a service can hold two keys: one **seals**, both **open**. Code: `pkg/secretbox` (`NewRing`, `Stale`, `Reseal`, `Settings`).

## Settings

| Variable | Meaning |
|---|---|
| `PAYRUDDER_SECRETS_KEY` | base64, 32 bytes: the key that seals every new box |
| `PAYRUDDER_SECRETS_KEY_ID` | its id, 1 to 255; empty is 1 |
| `PAYRUDDER_SECRETS_KEY_PREVIOUS` | a second key that only opens |
| `PAYRUDDER_SECRETS_KEY_PREVIOUS_ID` | its id; empty is 1 |

The charts, compose files and the Railway guide wire only `PAYRUDDER_SECRETS_KEY` (as `payments.secretsKey`); the other three are not in any of them yet, so set them as extra environment of payments-worker and plans-svc (Helm: the `env` map of each service in the values). The ids must differ. A service refuses to start on a key that is not base64 of 32 bytes, an id outside 1 to 255, equal ids, or a previous key without a key.

## Steps

Every replica of payments-worker and plans-svc gets the same values at each step. A replica that does not hold a box's key cannot open it (`ErrCorrupt`), so the new key is spread to all replicas **before** anything seals with it.

1. **Add.** Generate the new key (`openssl rand -base64 32`). Keep the old key sealing and give the new one as the second key: `SECRETS_KEY=<old>`, `SECRETS_KEY_PREVIOUS=<new>`, `SECRETS_KEY_PREVIOUS_ID=2`. (The name says "previous" because that is its job in step 2; here it only opens.) Roll it out to the end.
2. **Switch.** `SECRETS_KEY=<new>`, `SECRETS_KEY_ID=2`, `SECRETS_KEY_PREVIOUS=<old>` (id 1). New boxes carry id 2; old ones still open. Roll out.
3. **Re-seal.** Redo every stored box with the active key (below). The check that it is finished: no box has a first byte other than 2.
4. **Drop.** Remove `SECRETS_KEY_PREVIOUS` and its id. Back up the new key outside the platform (losing it makes every sealed credential unreadable; see [backup-restore.md](backup-restore.md)).

## Where boxes are, and what each is bound to

A re-seal must give `Reseal` the same associated data the writer used, or the box does not open.

| Service | Table.column | Bound to |
|---|---|---|
| payments-worker | `provider_accounts.settings_enc` | `tenant|provider|environment` (`store/accounts.go`) |
| payments-worker | `einvoice_accounts.settings_enc` | `tenant|einvoice|environment` for an integrator's primary account, `tenant|einvoice|environment|key` for a labelled one (`store/einvoice.go`) |
| plans-svc | `webhook_endpoints.secret_enc`, `prev_secret_enc` | `webhooks/model.SecretAAD(endpoint)` |
| plans-svc | `tenant_mail_integrations.api_key_enc` | `mailintegration/query.AAD(tenant)` |
| plans-svc | `user_totp.secret_enc` (two-factor sign-in, plan 22 G9) | `totp|<user id>` (`identity/command/twofactor.go`) |

The loop for each: select the rows whose column is `Stale` (first byte `<> active id`, `get_byte(col, 0) <> 2`), `Reseal(col, aad)`, and update with the old bytes in the `WHERE` so that a row an owner replaced in the meantime is left alone. In cells mode (ADR 0018) the tables are in the tenants' schemas: visit each place.

## What is built, and what is not

Built and tested: the key ids, a ring of keys, `Stale`, `Reseal`, the four variables and their checks (`pkg/secretbox`). **Not built: the job that runs the loop above** over these places. Until it exists, step 3 is not done and step 4 must wait; the platform runs correctly with two keys for as long as that takes (a credential an owner saves again is re-sealed with the active key by the ordinary write). Writing the job is about twenty lines per table; it has not been run against a real rotation.
