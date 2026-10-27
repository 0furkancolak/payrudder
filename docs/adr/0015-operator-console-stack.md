# ADR 0015: The operator console is a Vue single-page app

Status: accepted. Supersedes the Next.js choice made in Phase 12.

## Context

The console began as a Next.js (App Router) app. Its job is small and entirely client-side: it holds an operator token in the tab, calls the gateway, and shows tables and one guided
flow. It needs several languages, one of them right-to-left, and a structure that stays readable as screens are added.

## Decision

Vue 3 with Vite, TanStack Router for routing, TanStack Query for server state, vue-i18n for the languages, and a feature-based layout (`src/features/<feature>`).
The built files are static; nginx serves them and forwards `/gw` to the gateway.

## Why

- Nothing in the console needs server rendering. A Node server whose only server-side job was a proxy is more to run than static files behind nginx, and a smaller image.
- TanStack Query owns the cache, retries and polling (the playground follows an invoice through its saga) instead of a hand-written hook.
- TanStack Router has a Vue adapter (`@tanstack/vue-router`) with route-level `beforeLoad` guards, which is where the operator-token check belongs.
- A feature folder keeps one part of the product (pages, queries, routes) together, and cross-feature use goes through its `index.ts`.

## Consequences

- Route paths are not type-checked globally: registering the router for inference creates a type cycle between pages and the route tree in the Vue toolchain. Playwright covers navigation.
- The nginx image resolves the gateway per request, so it needs the container runtime's resolver (`NGINX_ENTRYPOINT_LOCAL_RESOLVERS`); on Kubernetes that is the cluster DNS.
- Translations are files in the repository; a unit test keeps them complete.

> Update: the static files are no longer served by nginx. See [ADR 0021](0021-console-on-cloudflare-workers.md).
