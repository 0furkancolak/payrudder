import { defineConfig } from "@playwright/test";

// The smoke test drives the real console against the real stack (make up): it needs the admin UI on ADMIN_URL, the gateway on
// PAYRUDDER_GATEWAY_URL and RabbitMQ's management API on RABBITMQ_URL. Nothing is mocked.
const baseURL = process.env.ADMIN_URL ?? "http://localhost:42301";

export default defineConfig({
  testDir: "./e2e",
  timeout: 5 * 60_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL,
    // These tests sign fresh people up and want the pages, not the welcome (the wizard and the tour that open by themselves): see NO_WELCOME_KEY.
    storageState: { cookies: [], origins: [{ origin: new URL(baseURL).origin, localStorage: [{ name: "payrudder.noWelcome", value: "1" }] }] },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
