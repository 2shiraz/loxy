import { defineConfig } from '@playwright/test';

// Runs against a live Loxy (`make dev`, or any all-in-one) — set LOXY_URL, LOXY_EMAIL, LOXY_PASSWORD,
// LOXY_SERVER_IP (a reachable server answering HTTP on :9001) and LOXY_HAPROXY_HOST (where frontend ports answer).
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
