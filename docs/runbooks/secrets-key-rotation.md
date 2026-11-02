# Replacing the secrets key

`PAYRUDDER_SECRETS_KEY` seals what the platform must be able to read back: tenants' provider and e-invoice credentials (payments-worker), webhook signing secrets and mail-integration keys (plans-svc). Until 2026-11 it was one key forever; a leak meant stopping the world. Now a sealed box carries the **id of its key** in its first byte (the byte was always `1` before, so every existing box is a box of key 1 and opens as before), and a service can hold two keys: one **seals**, both **open**. Code: `pkg/secretbox` (`NewRing`, `Stale`, `Reseal`, `Settings`) and the job that re-seals what is stored, `pkg/secretbox/reseal`, run as `payrudderctl secrets reseal`.

## Settings

| Variable | Meaning |
|---|---|
| `PAYRUDDER_SECRETS_KEY` | base64, 32 bytes: the key that seals every new box |
| `PAYRUDDER_SECRETS_KEY_ID` | its id, 1 to 255; empty is 1 |
| `PAYRUDDER_SECRETS_KEY_PREVIOUS` | a second key that only opens |
| `PAYRUDDER_SECRETS_KEY_PREVIOUS_ID` | its id; empty is 1 |

They are wired where `PAYRUDDER_SECRETS_KEY` is: in the compose file (as `PAYRUDDER_SECRETS_KEY_ID`, `_PREVIOUS` and `_PREVIOUS_ID`, empty by default), in the Helm chart (`payments.secretsKeyId`, `payments.secretsKeyPrevious`, `payments.secretsKeyPreviousId`, empty by default, rendered into the Secrets of payments-worker and plans-svc beside `payments.secretsKey`) and in the Railway guide (set on payments-worker, referenced from plans-svc; not exercised on Railway). The ids must differ. A service refuses to start on a key that is not base64 of 32 bytes, an id outside 1 to 255, equal ids, or a previous key without a key.

## Steps

Every replica of payments-worker and plans-svc gets the same values at each step. A replica that does not hold a box's key cannot open it (`ErrCorrupt`), so the new key is spread to all replicas **before** anything seals with it.

1. **Add.** Generate the new key (`openssl rand -base64 32`). Keep the old key sealing and give the new one as the second key: `SECRETS_KEY=<old>`, `SECRETS_KEY_PREVIOUS=<new>`, `SECRETS_KEY_PREVIOUS_ID=2`. (The name says "previous" because that is its job in step 2; here it only opens.) Roll it out to the end.
2. **Switch.** `SECRETS_KEY=<new>`, `SECRETS_KEY_ID=2`, `SECRETS_KEY_PREVIOUS=<old>` (id 1). New boxes carry id 2; old ones still open. Roll out.
3. **Re-seal.** Run `payrudderctl secrets reseal` once for each service (below). The check that it is finished is its own last line: every box is sealed with the active key. Run it with `--dry-run` first if you want to see that every box opens.
4. **Drop.** Remove `SECRETS_KEY_PREVIOUS` and its id. Back up the new key outside the platform (losing it makes every sealed credential unreadable; see [backup-restore.md](backup-restore.md)).

## The re-seal job

```
payrudderctl secrets reseal --service plans    [--dry-run] [--batch 200]
payrudderctl secrets reseal --service payments [--dry-run] [--batch 200]
```

Run it where the service runs (a job, a bastion), **with that service's environment**: `PAYRUDDER_PG_SYSTEM_DSN` (the system role of its database), `PAYRUDDER_CELLS` in cells mode (it visits the cell databases the service is given, with the system role of each), and the four `PAYRUDDER_SECRETS_KEY*` variables of step 2, so that the key that seals is the new one and the old one opens. `payments` covers `provider_accounts` and `einvoice_accounts`; `plans` covers the webhook endpoints' secrets, the mail keys and `user_totp` (table below). The system role of plans-svc may update only those box columns, since migration 00015.

- **What it does.** For each column it reads, in key order and `--batch` at a time, the boxes whose first byte is not the active key's id, opens each with the associated data of the table below and seals it again with the active key. In cells mode it does the same in the schema of every tenant of every cell, in a transaction per tenant. It prints, per column, the boxes, how many are on the active key now, how many it sealed again (with `--dry-run`: would), how many were replaced by their service while it ran, and how many did not open.
- **Safe to repeat and to stop.** Only boxes of another key are picked, so a run that was stopped (Ctrl-C, a crashed pod) is continued by running it again, and a finished one finds nothing. The update names the box it read: a credential an owner saved again in between keeps the owner's box (counted as *changed meanwhile*; the next run seals it if it is still on the old key). The same goes for a tenant moved between the pooled database and a cell while it ran: the next run finds the copy.
- **A box that does not open** (its key is not among the ones given, or the data differs) is counted, logged with its row's key and left as it is; the job then exits with an error and the **previous key must stay** until the box is dealt with (the tenant connects the account again, or the row is removed).
- **Not done while** a replica still seals with the old key: it would write new boxes of key 1 after the job finished. The job says so when boxes remain that are not on the active key (exit status 1); roll step 2 out to every replica, then run it again.
- **Without `--dry-run`** it writes; with it, nothing changes in the database.

## Where boxes are, and what each is bound to

The job opens a box with the associated data its writer used, so the data is written twice: where the service seals (the right-hand column) and in `pkg/secretbox/reseal/places.go`. The integration tests of payments-worker and plans-svc (`TestResealMovesEveryBoxToTheActiveKey`, `TestSecretsReseal`) store a box through the real writer of each place and have the job open it, so a service that changes its data breaks them. **A new place that calls `Seal` needs a line in `places.go` and a case in those tests.**

| Service | Table.column | Bound to | In |
|---|---|---|---|
| payments-worker | `provider_accounts.settings_enc` | `tenant\|provider\|environment` (`store/accounts.go`; `provider` is the account key, `stripe~eu` for a labelled one) | pooled database, tenant schemas |
| payments-worker | `einvoice_accounts.settings_enc` | `tenant\|einvoice\|environment` for an integrator's primary account, `tenant\|einvoice\|environment\|key` for a labelled one (`store/einvoice.go`) | pooled database, tenant schemas |
| plans-svc | `webhook_endpoints.secret_enc`, `prev_secret_enc` | `webhook:<endpoint id>` (`webhooks/model.SecretAAD`) | pooled database, tenant schemas |
| plans-svc | `tenant_mail_integrations.api_key_enc` | `mail-integration:<tenant id>` (`mailintegration/query.AAD`) | pooled database, tenant schemas |
| plans-svc | `user_totp.secret_enc` (two-factor sign-in, plan 22 G9) | `totp\|<user id>` (`identity/command/twofactor.go`) | pooled database only |

## If the key is lost (break-glass)

A deployment whose `PAYRUDDER_SECRETS_KEY` is gone (a Secret deleted, a vault emptied, a laptop with the only copy) cannot open what it sealed. **Written from the code; not drilled on a deployment.** What to do, in this order.

**1. Do not restart anything that still runs.** Every replica of payments-worker and plans-svc that is still up holds the key in its environment. Read it out before it restarts: `kubectl -n payrudder exec deploy/payments-worker -- printenv PAYRUDDER_SECRETS_KEY PAYRUDDER_SECRETS_KEY_ID` (compose: `docker compose exec payments-worker printenv ...`), put it in a vault, and the incident is over. Also look where the values were once written: the Helm values file or `my-secrets.yaml` (CI, a password manager), the old ReplicaSet's Secret (`kubectl get secret -o yaml`), the secret manager's history, Railway's variable history. A replica that already restarted without it is not lost: the key is only in the Secret you gave it.

**2. If it is really gone**, what was sealed cannot be recovered (the box is authenticated encryption; there is no master key). What breaks, and what replaces it:

| Sealed | Effect while unreadable | What brings it back |
|---|---|---|
| Provider credentials (`provider_accounts.settings_enc`) and e-invoice accounts (`einvoice_accounts.settings_enc`) | Charges, refunds and webhooks of the tenant through that account fail to open the settings (the error of `ErrCorrupt`, logged by payments-worker); the tenant is not charged and customers are not mailed receipts for them | The tenant enters the credentials again on the console's integrations page (the provider's card): saving writes a new box with the new key. Nothing else about the account (default, routing, labels) is lost |
| Webhook signing secrets (`webhook_endpoints.secret_enc`) | The dispatcher cannot sign, so every delivery to that endpoint fails and is retried; the previous secret of a roll is skipped silently | "Roll secret" on the endpoint in the console (or `POST /v1/webhook-endpoints/{id}/rotate-secret`): it writes a new current secret; the receiver must be given the new one, as in any roll |
| Mail-integration keys (`tenant_mail_integrations.api_key_enc`) | The tenant's own mail provider cannot be used | Disconnect and connect again on the console's mail page; disconnecting returns the tenant to Payrudder's own sender at once |
| Authenticator secrets (`user_totp.secret_enc`) | A code from an authenticator app is refused for everybody who has two-factor sign-in on; **recovery codes still work** (they are a hash kept with `PAYRUDDER_OTP_PEPPER`, not a box), so a person who kept theirs signs in, turns two-factor off with a recovery code and sets it up again | For a person without recovery codes, the operator's reset (Organisations, Manage, "Lost second factor", or `POST /admin/v1/users/two-factor/reset`), one person at a time. For everybody at once, as the last resort and with the services stopped: `DELETE FROM login_challenges; DELETE FROM user_recovery_codes; DELETE FROM user_totp;` as `plans_system` (everybody then signs in with the password alone and an owner of an account that requires it is sent to setup). This writes nothing to the audit log: write the incident down yourself |

**3. Start the services with a new key under a new id**, so that old boxes are recognised as foreign instead of looking like damaged ones: `openssl rand -base64 32`, `PAYRUDDER_SECRETS_KEY=<new>`, `PAYRUDDER_SECRETS_KEY_ID=2` on every replica of payments-worker and plans-svc at once (old boxes carry id 1 unless a rotation gave them another), no previous key. `payrudderctl secrets reseal --service payments --dry-run` (and `plans`) then lists every box that does not open, with its row's key: it is the inventory of who has to connect again. Do not run it without `--dry-run` (nothing opens, so nothing would change, and it exits with an error).

**4. Tell the tenants** which of the above they must redo (the inventory names them), and keep the new key in a vault **outside the platform** this time (the last step of the rotation above).

Why a lost key is unlike a leaked one: a leaked key needs the rotation above (add a second key, switch, re-seal, drop); a lost one has nothing to re-seal. Why recovery codes survive: `identity/command/twofactor.go` opens the authenticator secret only for a six-digit code, and hashes a recovery code with the pepper. What this section does not cover: a lost `PAYRUDDER_OTP_PEPPER` (every recovery code and every pending sign-in code stops working: the same operator reset applies), and the database backups, which hold boxes sealed with the old key (restoring one does not bring the key back).

## What is built, and what is not

Built and tested: the key ids, a ring of keys, `Stale`, `Reseal`, the four variables and their checks (`pkg/secretbox`); the job (`pkg/secretbox/reseal`, `payrudderctl secrets reseal`) with integration tests for both services in the pooled database and in a cell (two keys, a dry run, a second run, a box that does not open, a row saved again while the job ran, the grants of the plans-svc system role) and unit tests of the command's settings; the variables in the compose file, the Helm chart (rendered and tested) and the Railway guide.

Defaults, not agreed: one run per service with that service's environment (not one run over both databases); the system role, not the migrator, runs it (hence the narrow `UPDATE` grants of plans-svc, migration 00015 and its tenant twin); the job is a command to run by hand at step 3, not a scheduled or automatic one; a box that does not open stops the rotation (exit status 1) rather than being skipped silently; the job writes nothing to the audit log (`audit_events`), since it changes how a value is stored and not what it says.

**Not verified:** a real rotation on a deployment (the job ran against Postgres in containers and read-only against the local development database); cells on more than one cluster or behind a pooler (the cell test uses one cell on the test Postgres); a Helm install or a Railway deployment with the new variables (Helm was only rendered, Railway not touched); a large table (no timing was taken; each batch is one query over the rows not yet on the active key).
