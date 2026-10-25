# The operator console on Railway. Build context: the repo root (web/admin/Dockerfile expects web/admin as its context, which a Railway service
# built from the repo root does not have). Same steps as that file, the paths prefixed.
FROM node:24-alpine
WORKDIR /app
COPY web/admin/package.json web/admin/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/admin .
RUN npm run build
# Railway sets PORT; the service variable GATEWAY_URL overrides the default, which is the gateway's private address.
ENV PORT=3001 GATEWAY_URL=http://gateway.railway.internal:8000 PREFILL_PLAYGROUND=false BRAND_NAME=Payrudder PAYRUDDER_DEMO=false
CMD ["sh", "-c", "exec npx vite preview --host :: --port ${PORT} --strictPort"]
