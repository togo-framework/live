import { defineConfig, devices } from "@playwright/test";

const PORT = 8099;
const BASE_URL = `http://localhost:${PORT}`;

// The webServer builds+runs the example togo app (../) with the demo env vars,
// on a fixed SQLite database, before the tests run. Playwright waits for the
// chat board to answer before starting.
export default defineConfig({
  testDir: ".",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
  webServer: {
    // Run the example app in server-driven echo mode with a fresh DB. Uses a
    // prebuilt binary if present (fast); otherwise falls back to `go run .`.
    command:
      "rm -f ../live-example.e2e.db && (test -x /tmp/live-example-bin && /tmp/live-example-bin || GOFLAGS=-mod=mod go run .)",
    cwd: "..",
    url: `${BASE_URL}/api/live/agents`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    env: {
      DB_DRIVER: "sqlite",
      DATABASE_URL:
        "file:./live-example.e2e.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_time_format=sqlite",
      ADDR: `:${PORT}`,
      LIVE_MODE: "server",
      LIVE_RESPONDER: "echo",
      LIVE_SEED_AGENT: "Ada",
    },
  },
});
