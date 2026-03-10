import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global.setup.js',
  globalTeardown: './e2e/global.teardown.js',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: 'list',
  
  use: {
    baseURL: process.env.TP_UI_BASE_URL || 'http://127.0.0.1:3000',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  outputDir: process.env.TP_PLAYWRIGHT_ARTIFACT_DIR || './test-results',

  projects: [
    {
      name: 'blocking',
      testMatch: /.*\.blocking\.spec\.js/,
    },
    {
      name: 'regression',
      testMatch: /.*\.regression\.spec\.js/,
    },
  ],

  webServer: {
    command: 'PORTAL_API_TARGET=' + (process.env.TP_API_BASE_URL || 'http://127.0.0.1:1444') + ' npm run start',
    url: 'http://127.0.0.1:3000',
    reuseExistingServer: !process.env.CI,
    timeout: 120 * 1000,
  },
});
