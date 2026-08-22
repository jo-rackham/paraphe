import { defineConfig, devices } from "@playwright/test";

import { API_ORIGIN } from "./config.ts";

// End-to-end suite: a real PostgreSQL, the real Go binary, the real built
// interface, driven through a real browser.
//
// It exists for the failures the other suites cannot see. The Go tests prove
// the API answers correctly; the interface tests prove the components render.
// Neither notices that the API renamed a JSON key the interface still reads —
// which is precisely the risk whenever the data model is reshaped.

export default defineConfig({
  testDir: ".",
  // OUTSIDE .tmp, which the teardown wipes: traces and screenshots of a
  // failure must outlive the run that produced them.
  outputDir: ".results",
  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",
  // Campaigns are created and moderated by these tests: run them in order,
  // in one worker. Parallel runs would moderate the same request twice.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [["github"], ["list"]] : [["list"]],
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: API_ORIGIN,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // NO `reducedMotion` here: in @playwright/test 1.62.1 the option is a
    // silent no-op at every level — config, project, inline test.use — the
    // CDP trace shows prefers-reduced-motion sent as "no-preference" while
    // colorScheme beside it travels. Measured by an adversarial round; the
    // a11y sweep emulates it per page instead (07, emulateMedia), which is
    // the one channel that works.
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
