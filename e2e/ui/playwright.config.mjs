// Browser fixture for the inbox UI. e2e_test.go (TestInboxUI) starts the
// built binary and passes its address in CADUCEUS_URL.
import { defineConfig, devices } from '@playwright/test';

if (!process.env.CADUCEUS_URL) {
  throw new Error('CADUCEUS_URL is not set: run this fixture through `just e2e`');
}

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.mjs',
  // The tests share one Caduceus and clear its inbox, so they run one at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: 'list',
  timeout: 30_000,
  use: {
    baseURL: process.env.CADUCEUS_URL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
