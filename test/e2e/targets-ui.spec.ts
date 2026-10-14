import { test, expect, type Page } from '@playwright/test';

async function fixture(page: Page, failId = 0) {
  const targets = Array.from({ length: 4 }, (_, i) => ({
    id: i + 1, type: 'ip', ip: `10.0.0.${i + 1}`, weight: 100, backup: false,
    admin_state: i === 3 ? 'maint' : 'ready', removing: false,
    resolved: [{ slot_id: i + 1, address: `10.0.0.${i + 1}`, name: null,
      status: i === 3 ? 'unhealthy' : 'healthy', checks: [{ service_id: 1, status: i === 3 ? 'unhealthy' : 'healthy', check: 'L7OK/200 in 3ms' }] }],
  }));
  const calls: { id: number; body: Record<string, unknown>; method: string }[] = [];
  const project = { id: 1, name: 'cobalt', version: 1, algorithm: 'roundrobin', description: '', labels: {}, status: 'ready', maintenance: false,
    endpoints: [{ kind: 'service', url: 'http://10.0.0.1:20000', protocol: 'http', listen_port: 20000 }],
    counts: { targets: 4, services: 1, domains: 0 }, health: { status: 'degraded', healthy: 3, total: 4, messages: [] },
    stats: { requests_per_second: 857, open_connections: 280, bytes_out_per_second: 1000 } };
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const target = path.match(/\/targets\/(\d+)$/);
    if (target) {
      const id = Number(target[1]);
      const body = request.method() === 'DELETE' ? {} : request.postDataJSON();
      calls.push({ id, body, method: request.method() });
      if (id === failId) return route.fulfill({ status: 500, json: { error: { code: 'internal', message: 'Test failure' } } });
      Object.assign(targets[id - 1], body, request.method() === 'DELETE' ? { removing: true } : {});
      return route.fulfill({ json: { target: targets[id - 1] } });
    }
    const response = path.endsWith('/auth/me') ? { user: { id: 1, email: 'admin@loxy.local', role: 'owner' }, csrf_token: 'test' }
      : path.endsWith('/targets') ? { targets }
      : path.endsWith('/services') ? { services: [{ id: 1, protocol: 'http', listen_port: 20000 }] }
      : path.endsWith('/events') ? null : { project };
    if (response === null) return route.fulfill({ contentType: 'text/event-stream', body: ': keepalive\n\n' });
    await route.fulfill({ json: response });
  });
  await page.goto('/projects/1/targets');
  await expect(page.getByRole('checkbox', { name: 'Select 10.0.0.1', exact: true })).toBeVisible();
  return calls;
}

test('multi-select right-click applies only to selected targets; weight, backup, and keyboard menu', async ({ page }) => {
  const calls = await fixture(page);
  await page.getByRole('checkbox', { name: 'Select 10.0.0.1', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Select 10.0.0.2', exact: true }).check();
  await page.locator('tbody').first().click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Drain', exact: true }).click();
  await expect.poll(() => calls.length).toBe(2);
  expect(calls.map((c) => c.id)).toEqual([1, 2]);
  expect(calls.every((c) => c.body.admin_state === 'drain')).toBe(true);
  await page.getByRole('button', { name: 'Actions for 2' }).click();
  await page.getByRole('menuitem', { name: 'Set weight…' }).click();
  await page.getByLabel('Weight (0–256)').fill('10');
  await page.getByRole('button', { name: 'Save weight' }).click();
  await expect.poll(() => calls.filter((c) => c.body.weight === 10).length).toBe(2);
  await expect(page.getByRole('button', { name: 'Actions for 2', exact: true })).toBeVisible();
  const check = page.getByRole('checkbox', { name: 'Select 10.0.0.2', exact: true });
  await check.focus();
  await check.press('Shift+F10');
  await page.getByRole('menuitem', { name: 'Make backup', exact: true }).click();
  await expect.poll(() => calls.filter((c) => c.body.backup === true).length).toBe(2);
});

test('select-all, maintenance confirmation, partial failures, and clear selection', async ({ page }) => {
  const calls = await fixture(page, 2);
  await page.getByRole('checkbox', { name: 'Select all targets' }).check();
  await page.getByRole('button', { name: 'Actions for 4' }).click();
  await page.getByRole('menuitem', { name: 'Maintenance', exact: true }).click();
  expect(calls).toHaveLength(0);
  await page.getByRole('dialog').getByRole('button', { name: 'Maintenance', exact: true }).click();
  await expect.poll(() => calls.length).toBe(4);
  await expect(page.getByRole('button', { name: 'Actions for 1', exact: true })).toBeVisible();
  await expect(page.getByRole('checkbox', { name: 'Select 10.0.0.2', exact: true })).toBeChecked();
  await expect(page.getByRole('checkbox', { name: 'Select 10.0.0.1', exact: true })).not.toBeChecked();
  await page.getByRole('button', { name: 'Clear selection' }).click();
  await expect(page.getByRole('checkbox', { name: 'Select all targets' })).not.toBeChecked();
});

test('shift range and right-click on an unselected target replace the selection', async ({ page }) => {
  const calls = await fixture(page);
  await page.getByRole('checkbox', { name: 'Select 10.0.0.1', exact: true }).check();
  await page.getByRole('checkbox', { name: 'Select 10.0.0.3', exact: true }).click({ modifiers: ['Shift'] });
  await expect(page.getByRole('button', { name: 'Actions for 3' })).toBeVisible();
  await page.locator('tbody').nth(3).click({ button: 'right' });
  await page.getByRole('menuitem', { name: 'Enable', exact: true }).click();
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0].id).toBe(4);
  await page.getByRole('button', { name: 'Actions for 1', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Remove', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(calls.filter((c) => c.method === 'DELETE')).toHaveLength(0);
  await page.getByRole('button', { name: 'Actions for 1', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Remove', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Remove targets', exact: true }).click();
  await expect.poll(() => calls.filter((c) => c.method === 'DELETE').length).toBe(1);
  await expect(page.getByRole('checkbox', { name: 'Select 10.0.0.4', exact: true })).toBeDisabled();
});

test('wide, tablet, mobile, and dark layouts fill the shell without page overflow', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await fixture(page);
  for (const width of [2048, 900, 390]) {
    await page.setViewportSize({ width, height: 900 });
    const bounds = await page.locator('main').boundingBox();
    expect(bounds).not.toBeNull();
    expect(Math.abs(bounds!.x + bounds!.width - width)).toBeLessThan(2);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: test.info().outputPath(`targets-${width}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 2048, height: 900 });
  await page.evaluate(() => document.documentElement.dataset.theme = 'dark');
  await page.screenshot({ path: test.info().outputPath('targets-dark.png'), fullPage: true });
  expect(errors).toEqual([]);
});
