import tailwindcss from "@tailwindcss/vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

// The console calls the API under /gw. In production the Cloudflare Worker forwards it (worker/proxy.ts); in development and under `vite preview`
// (the e2e container, deploy/railway) Vite does the same. BRAND_NAME, QUOTIFY_DEMO and PREFILL_PLAYGROUND feed /env.js outside the Worker.
const gateway = (process.env.GATEWAY_URL ?? "http://localhost:42800").replace(/\/$/, "");
const proxy = { "/gw": { target: gateway, changeOrigin: true, rewrite: (p: string) => p.replace(/^\/gw/, "") } };

/** Answers /env.js under `vite preview` from the environment, as the Worker does from its vars. */
const previewEnv = (): Plugin => ({
  name: "console-preview-env",
  configurePreviewServer(server) {
    server.middlewares.use("/env.js", (_req, res) => {
      const brand = (process.env.BRAND_NAME ?? "").replace(/[^\p{L}\p{N} ._-]/gu, "");
      const cfg = { prefill: process.env.PREFILL_PLAYGROUND === "true", brand, demo: process.env.QUOTIFY_DEMO === "true" };
      res.setHeader("content-type", "application/javascript");
      res.setHeader("cache-control", "no-store");
      res.end(`window.__QUOTIFY__=${JSON.stringify(cfg)};`);
    });
  },
});

export default defineConfig({
  plugins: [vue(), tailwindcss(), previewEnv()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy },
  preview: { proxy, allowedHosts: true },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
