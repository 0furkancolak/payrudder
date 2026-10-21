# The operator console. Build context: web/admin.
FROM node:24-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM node:24-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

FROM nginx:1.29-alpine
ENV PORT=3001 NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1 GATEWAY_URL=http://gateway:8000 PREFILL_OPERATOR_TOKEN= BRAND_NAME=Quotify QUOTIFY_DEMO=false
COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 3001
