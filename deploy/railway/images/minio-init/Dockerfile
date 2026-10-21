# One-shot job: buckets and lifecycle rules in MinIO. Build context: the repo root. Same pinned client as the compose stack.
FROM cgr.dev/chainguard/minio-client@sha256:d8a93c5d335509ff353774eefec8f82fa2a2a975e800139702232d8be3dbe6b9
COPY deploy/railway/images/minio-init/init.sh /init.sh
ENTRYPOINT ["bash", "/init.sh"]
