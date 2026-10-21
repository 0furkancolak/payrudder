import { defineConfig } from "@playwright/test";

// The billing pages against a mocked gateway: the built console is served by `vite preview` and every /gw request is answered by the test.
// No stack is needed, so these run in the console's CI job; the tests against the real stack are in e2e/ (playwright.config.ts).
// MOCK_PORT lets two checkouts run this suite at the same time (each serves its own build).
const port = process.env.MOCK_PORT ?? "3003";

export default defineConfig({
  testDir: "./e2e-mock",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: true,
  reporter: [["list"]],
  use: { baseURL: `http://localhost:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: {
    command: `npm run build && npx vite preview --port ${port} --strictPort`,
    url: `http://localhost:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
