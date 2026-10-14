import { test, expect, type Page } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';
const serverIP = process.env.LOXY_SERVER_IP ?? '10.0.0.10';
const haHost = process.env.LOXY_HAPROXY_HOST ?? '127.0.0.1';
const name = `e2e-${Date.now().toString(36)}`;
const port = 18000 + Math.floor(Math.random() * 1000);
const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/dashboard/);
}

async function actions(page: Page, item: string) {
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: item }).click();
}

test('project: create via wizard, serve traffic, maintenance, delete', async ({ page, request }) => {
  await login(page);
  const status = async () => (await request.get(`http://${haHost}:${port}/`).catch(() => null))?.status();

  // Wizard: name, one IP target answering on :9001, HTTP service on an explicit port.
  await page.goto('/projects/new');
  await page.getByLabel('Name', { exact: true }).fill(name);
  await page.getByLabel('Addresses, one per line').fill(serverIP);
  await page.getByLabel('Public port', { exact: true }).fill(String(port));
  await page.getByLabel('Server port', { exact: true }).fill('9001');
  await page.getByRole('button', { name: 'Create project' }).click();
  await expect(page).toHaveURL(/\/projects\/\d+\/overview$/);
  const base = page.url().replace(/\/overview$/, '');

  // Targets tab: the IP target turns healthy.
  await page.goto(`${base}/targets`);
  const row = page.getByRole('row', { name: new RegExp(`${esc(serverIP)}(?!\\d)`) }).first();
  await expect(row).toBeVisible();
  await expect(row).toContainText('Healthy', { timeout: 30_000 });
  await expect.poll(status, { timeout: 10_000 }).toBe(200);

  // Maintenance → 503, then off again.
  await actions(page, 'Turn maintenance on');
  await page.getByRole('dialog').getByRole('button', { name: 'Turn maintenance on' }).click();
  await expect.poll(status, { timeout: 5_000 }).toBe(503);
  await actions(page, 'Turn maintenance off');
  await page.getByRole('dialog').getByRole('button', { name: 'Turn maintenance off' }).click();
  await expect.poll(status, { timeout: 5_000 }).toBe(200);

  // Delete with typed confirmation; the port stops answering.
  await actions(page, 'Delete…');
  await page.getByLabel(/Type .* to confirm/).fill(name);
  await page.getByRole('dialog').getByRole('button', { name: 'Delete project' }).click();
  await expect(page).toHaveURL(/\/projects$/);
  await expect(page.getByRole('link', { name, exact: true })).toHaveCount(0);
  await expect.poll(async () => (await request.get(`http://${haHost}:${port}/`).catch(() => null)) === null, { timeout: 10_000 }).toBe(true);
});
