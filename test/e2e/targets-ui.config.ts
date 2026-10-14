import { defineConfig } from '@playwright/test';

// UI-only regression tests with mocked REST responses against a running Vite server.
export default defineConfig({
  testMatch: 'targets-ui.spec.ts',
  workers: 1,
  webServer: {
    command: 'npm --prefix ../../web run dev -- --host 127.0.0.1 --port 5173',
    url: 'http://127.0.0.1:5173',
    reuseExistingServer: !process.env.CI,
  },
  use: {
    baseURL: process.env.LOXY_UI_URL ?? 'http://127.0.0.1:5173',
    launchOptions: process.env.LOXY_CHROMIUM ? { executablePath: process.env.LOXY_CHROMIUM } : {},
  },
});
