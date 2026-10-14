import { test, expect } from '@playwright/test';

const email = process.env.LOXY_EMAIL ?? 'admin@loxy.local';
const password = process.env.LOXY_PASSWORD ?? '';

test('domains: attach a domain to a project, see DNS status, remove it', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).toHaveURL(/dashboard/);

  // A project to attach the domain to, created through the API with the session's CSRF token.
  const me = await (await page.request.get('/api/v1/auth/me')).json();
  const h = { 'X-LOXY-CSRF': me.csrf_token };
  const tag = Date.now().toString(36);
  const created = await (await page.request.post('/api/v1/projects', { headers: h,
    data: { name: `e2e-dom-${tag}`, service: { protocol: 'http', destination_port: 9001 } } })).json();
  const pid = created.project?.id;
  expect(pid).toBeTruthy();

  const fqdn = `e2e-${tag}.example.invalid`;
  await page.goto(`/projects/${pid}/domains`);
  await page.getByLabel('Domain name').fill(fqdn);
  await page.getByRole('button', { name: 'Add domain' }).click();

  const card = page.locator('details', { hasText: fqdn });
  await expect(card).toBeVisible();
  // .invalid never resolves → "No record yet" (or a resolver error), never "Resolves correctly".
  await expect(card).not.toContainText('Resolves correctly');
  await expect(card.getByRole('button', { name: 'Check now' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Issue certificate' })).toBeDisabled();
  await expect(card.getByText('DNS records to create')).toBeVisible();

  await card.getByRole('button', { name: 'Remove domain' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Remove domain' }).click();
  await expect(page.locator('details', { hasText: fqdn })).toHaveCount(0);

  expect((await page.request.delete(`/api/v1/projects/${pid}`, { headers: h })).status()).toBe(204);
});
