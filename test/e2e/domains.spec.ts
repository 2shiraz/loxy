import { test, expect } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';

test('domains: add a domain, see required records and verification status (§8.16)', async ({ page, request }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/load-balancers/);

  const fqdn = `e2e-${Date.now().toString(36)}.example.invalid`;
  await page.goto('/domains');
  await page.getByRole('button', { name: 'Add domain' }).first().click();
  await page.getByLabel('Domain name').fill(fqdn);
  await page.getByLabel('Load balancer').selectOption({ index: 1 });
  await page.getByRole('dialog').getByRole('button', { name: 'Add domain' }).click();
  const row = page.getByRole('button', { name: fqdn });
  await expect(row).toBeVisible();
  // .invalid never resolves → "Missing" (or a resolver error), never "Resolves correctly".
  await expect(page.getByRole('row', { name: new RegExp(fqdn.replace(/\./g, '\\.')) })).not.toContainText('Resolves correctly');
  await expect(page.getByText('Check now')).toBeVisible();
  await expect(page.getByRole('button', { name: /Issue certificate/ })).toBeDisabled();
  // clean up through the API UI flow
  await page.getByRole('button', { name: 'Remove domain' }).click();
  await page.getByRole('dialog').last().getByRole('button', { name: /Remove/ }).last().click();
  await expect(page.getByRole('button', { name: fqdn })).toHaveCount(0);
  void request;
});
