import { test, expect, type Page } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';
const serverIP = process.env.LOXY_SERVER_IP ?? '10.0.0.10';
const haHost = process.env.LOXY_HAPROXY_HOST ?? '127.0.0.1';
const name = `e2e-${Date.now().toString(36)}`;
const port = 18000 + Math.floor(Math.random() * 1000);

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/dashboard/);
}

test('backend + frontend: create, serve traffic, maintenance, delete', async ({ page, request }) => {
  await login(page);
  const status = async () => (await request.get(`http://${haHost}:${port}/`).catch(() => null))?.status();

  // Backend (§8.4) with one IP server (§8.6).
  await page.goto('/backends/new');
  await page.getByLabel('Name').fill(name);
  await page.getByLabel('Default server port').fill('9001');
  await page.getByRole('button', { name: 'Create backend' }).click();
  await expect(page).toHaveURL(/backends\/\d+/);
  await page.getByRole('button', { name: 'Add servers' }).first().click();
  await page.getByLabel('Addresses, one per line').fill(serverIP);
  await page.getByRole('dialog').getByRole('button', { name: 'Add server' }).click();
  await expect(page.getByText(serverIP).first()).toBeVisible();
  await expect(page.getByText('Healthy').first()).toBeVisible({ timeout: 30_000 });

  // Frontend (§8.2) on a free port, default backend = the new backend.
  await page.goto('/frontends/new');
  await page.getByLabel('Name').fill(name);
  await page.getByLabel('Port').first().fill(String(port));
  await page.getByLabel('Default backend').selectOption({ label: `${name} · :9001` });
  await page.getByRole('button', { name: 'Create frontend' }).click();
  await expect(page).toHaveURL(/frontends\/\d+/);
  await expect.poll(status, { timeout: 10_000 }).toBe(200);

  // Maintenance → 503, then off again.
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Turn maintenance on' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Turn maintenance on' }).click();
  await expect.poll(status, { timeout: 5_000 }).toBe(503);
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Turn maintenance off' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Turn maintenance off' }).click();
  await expect.poll(status, { timeout: 5_000 }).toBe(200);

  // Delete with typed confirmation; the backend is freed and can be deleted too.
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Delete…' }).click();
  await page.getByLabel(/Type .* to confirm/).fill(name);
  await page.getByRole('dialog').getByRole('button', { name: 'Delete frontend' }).click();
  await expect(page).toHaveURL(/\/frontends$/);
  await expect(page.getByText(name)).toHaveCount(0);
  await expect.poll(async () => (await request.get(`http://${haHost}:${port}/`).catch(() => null)) === null, { timeout: 10_000 }).toBe(true);
});
