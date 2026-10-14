import { test, expect, type Page } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';
const targetIP = process.env.LOXY_TARGET_IP ?? '10.0.0.10';
const lbHost = process.env.LOXY_LB_HOST ?? '127.0.0.1';
const name = `e2e-${Date.now().toString(36)}`;
const port = 18000 + Math.floor(Math.random() * 1000);

async function login(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/load-balancers/);
}

test('login, create LB via wizard, serve traffic, maintenance, delete', async ({ page, request }) => {
  await login(page);

  // Wizard (§8.2): HTTP template + one IP target.
  await page.goto('/load-balancers/new');
  await page.getByRole('button', { name: /^HTTP/ }).first().click();
  await page.getByLabel('Source port').first().fill(String(port));
  await page.getByLabel('Destination port').first().fill('9001');
  await page.getByRole('radio', { name: 'IP' }).click();
  await page.getByLabel('IP addresses, one per line').fill(targetIP);
  await page.getByLabel('Name', { exact: true }).fill(name);
  await page.getByRole('button', { name: 'Create & apply' }).click();
  await expect(page).toHaveURL(/load-balancers\/\d+/);
  await expect(page.getByText(name).first()).toBeVisible();

  // Traffic flows within a few seconds (§8.2 acceptance: reachable within 3 s).
  await expect.poll(async () => (await request.get(`http://${lbHost}:${port}/`).catch(() => null))?.status(), { timeout: 5_000 }).toBe(200);

  // Services tab shows the target healthy.
  await page.getByRole('link', { name: 'Services' }).click();
  await expect(page.getByText(targetIP)).toBeVisible();
  await expect(page.getByText('Healthy').first()).toBeVisible({ timeout: 20_000 });

  // Maintenance mode → 503, then off again.
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Maintenance mode on' }).click();
  await page.getByRole('button', { name: 'Enable maintenance' }).click();
  await expect.poll(async () => (await request.get(`http://${lbHost}:${port}/`)).status(), { timeout: 5_000 }).toBe(503);
  await page.getByRole('button', { name: 'Actions', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Maintenance mode off' }).click();
  await page.getByRole('button', { name: 'Disable maintenance' }).click();
  await expect.poll(async () => (await request.get(`http://${lbHost}:${port}/`)).status(), { timeout: 5_000 }).toBe(200);

  // Delete with typed confirmation (§8.10).
  await page.getByRole('link', { name: 'Delete' }).click();
  await page.getByRole('button', { name: 'Delete load balancer' }).click();
  await page.getByLabel(/Type .* to confirm/).fill(name);
  await page.getByRole('dialog').getByRole('button', { name: 'Delete load balancer' }).click();
  await expect(page).toHaveURL(/\/load-balancers$/);
  await expect(page.getByText(name)).toHaveCount(0);
});
