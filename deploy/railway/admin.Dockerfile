# The operator console on Railway. Build context: the repo root (web/admin/Dockerfile expects web/admin as its context, which a Railway service
# built from the repo root does not have). Same stages as that file, the paths prefixed.
FROM node:24-alpine AS deps
WORKDIR /app
COPY web/admin/package.json web/admin/package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM node:24-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY web/admin .
RUN npm run build

FROM nginx:1.29-alpine
# Railway sets PORT; the service variable GATEWAY_URL overrides the default, which is the gateway's private address.
ENV PORT=3001 NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1 GATEWAY_URL=http://gateway.railway.internal:8000 PREFILL_PLAYGROUND=false BRAND_NAME=Payrudder QUOTIFY_DEMO=false
COPY web/admin/nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
