# ADR 0013 — S3-compatible object storage for invoice PDFs

- Status: Accepted
- Date: 2026-10-17

## Context

invoice-worker produces PDFs that must be stored durably, served through short-lived links, and written idempotently
(deterministic keys). The platform runs on Docker Compose and kind locally, and should be deployable to any cloud later without code changes.

## Decision

- Store PDFs in **S3-compatible object storage**, accessed only through the S3 API (`aws-sdk-go-v2/service/s3`) behind
  `pkg/blob` (`Put`, `Head`, `PresignGet`).
- **MinIO** is the implementation in Compose, testcontainers and kind (Helm, in-cluster). Configuration:
  `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_FORCE_PATH_STYLE`, credentials from a Secret.
- No MinIO-specific SDK calls or admin APIs in service code. Bucket creation and lifecycle rules are set up by deployment
  tooling (an init container in Compose, a Helm hook Job in Kubernetes).

## Alternatives considered

| Option | Why not |
|---|---|
| `bytea` in a worker database | Simple, but bloats Postgres backups, and presigned download links would need a custom endpoint |
| MinIO SDK (`minio-go`) | Works with S3 too, but makes it tempting to use MinIO-only features |
| Local filesystem / PVC | Not shared across replicas, no presigned URLs |

## Consequences

- Moving to AWS S3, Cloudflare R2 or Ceph RGW is configuration only. A CI job runs the blob contract tests against MinIO.
- The presigned URL host must be reachable by the API client. In kind that's the MinIO Service exposed through the ingress/port-forward (documented in 10-local-dev).
