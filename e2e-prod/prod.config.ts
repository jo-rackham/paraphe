import { defineConfig, devices } from "@playwright/test";

// The PRODUCTION smoke suite: real URLs, the real ingress, the real bytes a
// volunteer receives. It exists for what the local end-to-end suite cannot
// see — TLS, the ingress's headers, the published lists, the CDN-less asset
// path — and it runs against a LIVE instance, so it is written to leave
// nothing behind: no campaign, no account, no public request, ever.
//
//   task e2e-prod
//
// Read-only journeys and the account-less version run as-is. The signed-in
// journeys need a probe campaign's coordination, supplied by environment —
// see prod.spec.ts — and skip out loud without it.

export default defineConfig({
  testDir: ".",
  outputDir: ".results",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [["list"]],
  timeout: 45_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: (process.env.PARAPHE_PROD_ORIGIN ?? "https://paraphe.org").trim(),
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
