import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

// In production nginx forwards /gw to the gateway (nginx/default.conf.template); in development Vite does the same.
const gateway = (process.env.GATEWAY_URL ?? "http://localhost:8000").replace(/\/$/, "");

export default defineConfig({
  plugins: [vue()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy: { "/gw": { target: gateway, changeOrigin: true, rewrite: (p) => p.replace(/^\/gw/, "") } } },
  test: { environment: "node", include: ["src/**/*.test.ts"] },
});
