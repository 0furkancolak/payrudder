import { defineConfig } from "@playwright/test";

// The billing pages against a mocked gateway: the built console is served by `vite preview` and every /gw request is answered by the test.
// No stack is needed, so these run in the console's CI job; the tests against the real stack are in e2e/ (playwright.config.ts).
export default defineConfig({
  testDir: "./e2e-mock",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  reporter: [["list"]],
  use: { baseURL: "http://localhost:3003", trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: {
    command: "npm run build && npx vite preview --port 3003 --strictPort",
    url: "http://localhost:3003",
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
