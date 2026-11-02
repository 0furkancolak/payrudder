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
  // CI: a spec that timed out once (a runner hiccup: three different forms timed out at 60 s in successive runs, each fine alone) gets one more try and is reported as flaky,
  // with a trace of the first attempt; a spec that fails twice fails the job.
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"]],
  use: { baseURL: `http://localhost:${port}`, trace: process.env.CI ? "on-first-retry" : "retain-on-failure", screenshot: "only-on-failure" },
  webServer: {
    command: `npm run build && npx vite preview --port ${port} --strictPort`,
    url: `http://localhost:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
