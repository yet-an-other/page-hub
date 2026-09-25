import { defineConfig, devices } from '@playwright/test'

const port = 4179
const assertion = 'playwright-only-assertion'

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  // One retry in CI keeps the release gate from failing on a single
  // scheduling hiccup (e.g. a browser worker stalled under runner load).
  retries: process.env.CI ? 1 : 0,
  reporter: 'line',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    extraHTTPHeaders: {
      'X-Page-Hub-Assertion': assertion,
    },
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    {
      name: 'chromium-narrow',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 } },
    },
  ],
  webServer: {
    command: `cd .. && CGO_ENABLED=0 go build -o /tmp/page-hub-playwright ./cmd/page-hub && go run ./tools/seedcatalog -catalog /tmp/page-hub-playwright-catalog.db && PAGE_HUB_CATALOG_PATH=/tmp/page-hub-playwright-catalog.db PAGE_HUB_LISTEN_ADDR=127.0.0.1:${port} PAGE_HUB_AUTH_ASSERTION_VALUE=${assertion} PAGE_HUB_STORAGE_QUOTA_BYTES=1048576 PAGE_HUB_PUBLIC_BASE_URL=http://127.0.0.1:${port} /tmp/page-hub-playwright`,
    url: `http://127.0.0.1:${port}/_page-hub/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
