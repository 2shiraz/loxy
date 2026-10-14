import { defineConfig } from '@playwright/test';

// Runs against a live Loxy (`make dev`, or any all-in-one) — set LOXY_URL, LOXY_EMAIL, LOXY_PASSWORD,
// and LOXY_TARGET_IP (a reachable backend serving HTTP on :9001) / LOXY_LB_HOST (where LB ports answer).
export default defineConfig({
  testDir: '.',
  timeout: 60_000,
  workers: 1,
  use: {
    baseURL: process.env.LOXY_URL ?? 'http://127.0.0.1:9090',
    viewport: { width: 1360, height: 860 },
    launchOptions: process.env.LOXY_CHROMIUM ? { executablePath: process.env.LOXY_CHROMIUM } : {},
  },
});
