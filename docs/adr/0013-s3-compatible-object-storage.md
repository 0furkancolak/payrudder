# ADR 0013 — S3-compatible object storage for invoice PDFs

- Status: Accepted (amended 2026-10-17: multipart, multi-file asset uploads)
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

**Amendment: assets.** Tenant files (logos, attachments, general files) use the same storage through
[assets-svc](../03-services/assets-svc.md). Uploads are **multipart and multi-file**:
- Large or many files: a session of ≤ 50 files with **presigned S3 multipart** (`CreateMultipartUpload` → presigned `UploadPart`
  → `CompleteMultipartUpload`). Clients upload parts in parallel straight to S3, and uploads can resume.
- Small files: `multipart/form-data` with ≤ 20 files per request, streamed by the gateway into a server-side S3 multipart upload.
- `pkg/blob` therefore also exposes `CreateMultipart`, `PresignPart`, `ListParts`, `CompleteMultipart`, `AbortMultipart` and a
  streaming `PutStream` (SDK upload manager). MinIO implements all of these S3 operations, so local and cloud behave the same.

**Image note (2026-10-17).** MinIO no longer publishes community images to Docker Hub or Quay. Compose and testkit use
Chainguard's MinIO build (`cgr.dev/chainguard/minio`), pinned by digest because its free tier only tags `:latest`. MinIO doesn't
implement the `AbortIncompleteMultipartUpload` lifecycle element, so locally `MINIO_API_STALE_UPLOADS_EXPIRY=48h` is the
backstop for abandoned multipart uploads (on AWS S3 it's the lifecycle rule). If the image becomes unavailable, any S3-compatible
server works without code changes, because services only speak the S3 API.

## Alternatives considered

| Option | Why not |
|---|---|
| `bytea` in a worker database | Simple, but bloats Postgres backups, and presigned download links would need a custom endpoint |
| MinIO SDK (`minio-go`) | Works with S3 too, but makes it tempting to use MinIO-only features |
| Local filesystem / PVC | Not shared across replicas, no presigned URLs |
| Single-request uploads only (`PutObject` via the gateway) | Bytes flow through Quotify, uploads can't resume, and 5 GiB is S3's single-PUT limit |
| tus / resumable protocol server | Another protocol and component. S3 multipart already gives resumable, parallel parts |

## Consequences

- Moving to AWS S3, Cloudflare R2 or Ceph RGW is configuration only. A CI job runs the blob contract tests against MinIO.
- The presigned URL host must be reachable by the API client. In kind that's the MinIO Service exposed through the ingress/port-forward (documented in 10-local-dev).
