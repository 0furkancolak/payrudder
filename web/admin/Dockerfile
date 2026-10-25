# The console for the end-to-end tests and the Railway demo: the built app served by `vite preview`, which also forwards /gw to the gateway
# (GATEWAY_URL) and answers /env.js. Production runs on Cloudflare Workers instead (wrangler.jsonc, docs/adr/0021-console-on-cloudflare-workers.md).
# Build context: web/admin.
FROM node:24-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build
ENV PORT=3001 GATEWAY_URL=http://gateway:8000 PREFILL_PLAYGROUND=false BRAND_NAME=Payrudder QUOTIFY_DEMO=false
EXPOSE 3001
# "::" listens on IPv4 and IPv6 (Railway's private network is IPv6).
CMD ["sh", "-c", "exec npx vite preview --host :: --port ${PORT} --strictPort"]
