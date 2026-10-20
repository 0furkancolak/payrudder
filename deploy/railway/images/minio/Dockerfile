# S3-compatible storage for Railway (ADR 0013); skip it when you point S3_* at Cloudflare R2, Tigris or AWS. Build context: the repo root.
# Same pinned Chainguard build as deploy/compose/compose.yaml. Mount a volume at /data and set RAILWAY_RUN_UID=0.
# Service variables: MINIO_ROOT_USER, MINIO_ROOT_PASSWORD, PORT=9000 (the S3 API, also the public domain: presigned links point at it; health: /minio/health/live).
FROM cgr.dev/chainguard/minio@sha256:c93745c56fccdaf0e5dcb88999a96a65a136428beb045ef5d546255a80d747a7
# MinIO's equivalent of an AbortIncompleteMultipartUpload lifecycle rule (assets-svc backstop).
ENV MINIO_API_STALE_UPLOADS_EXPIRY=48h MINIO_API_STALE_UPLOADS_CLEANUP_INTERVAL=6h
# ":9000" listens on IPv4 and IPv6.
CMD ["server", "/data", "--address", ":9000", "--console-address", ":9001"]
