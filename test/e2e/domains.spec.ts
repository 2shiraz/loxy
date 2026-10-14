import { test, expect } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';

test('domains: add a domain, see required records and verification status (§8.9)', async ({ page, request }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/dashboard/);

  // A frontend to attach the domain to (created through the API with the session's CSRF token).
  const me = await (await page.request.get('/api/v1/auth/me')).json();
  const h = { 'X-LOXY-CSRF': me.csrf_token };
  const tag = Date.now().toString(36);
  const be = await (await page.request.post('/api/v1/backends', { headers: h, data: { name: `e2e-dom-${tag}`, mode: 'http', port: 9001 } })).json();
  const fe = await (await page.request.post('/api/v1/frontends', { headers: h,
    data: { name: `e2e-dom-${tag}`, mode: 'http', binds: [{ address: '*', port: 19000 + Math.floor(Math.random() * 500) }], default_backend_id: be.backend.id } })).json();
  expect(fe.frontend?.id).toBeTruthy();

  const fqdn = `e2e-${tag}.example.invalid`;
  await page.goto('/domains');
  await page.getByRole('button', { name: 'Add domain' }).first().click();
  await page.getByLabel('Domain name').fill(fqdn);
  await page.getByLabel('Frontend').selectOption({ label: `e2e-dom-${tag} (HTTP)` });
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
  await expect.poll(async () => (await page.request.delete(`/api/v1/frontends/${fe.frontend.id}`, { headers: h })).status(), { timeout: 10_000 }).toBe(204);
  expect((await page.request.delete(`/api/v1/backends/${be.backend.id}`, { headers: h })).status()).toBe(204);
  void request;
});
