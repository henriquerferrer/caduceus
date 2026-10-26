// The inbox shows a Verify message's channel and its verification's live
// status. Each test clears the inbox first and drives Caduceus through its
// emulated Verify endpoint and its REST API.
import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const service = 'VAe2e00000000000000000000000000000';

test.beforeEach(async ({ request }) => {
  expect((await request.delete('/api/v1/messages')).status()).toBe(204);
});

// startVerification sends a code through the emulated Verify endpoint and
// returns the verification's sid and code.
async function startVerification(request, to) {
  const res = await request.post(`/v2/Services/${service}/Verifications`, { form: { To: to, Channel: 'sms' } });
  expect(res.status()).toBe(201);
  const { sid } = await res.json();
  const listed = await request.get('/api/v1/verifications?to=' + encodeURIComponent(to));
  const v = (await listed.json()).verifications.find(v => v.id === sid);
  return { sid, code: v.code };
}

// row is the message bubble in the open thread that carries the code.
const row = (page, text) => page.locator('#thread .msg').filter({ hasText: text });

// openThread opens the conversation with a number from the side list.
async function openThread(page, number) {
  await page.getByRole('navigation', { name: 'Conversations' }).getByRole('button', { name: number }).click();
}

test('a Verify message shows its channel and live status without a reload', async ({ page, request }) => {
  await page.goto('/');
  // A reload would drop this marker.
  await page.evaluate(() => { window.notReloaded = true; });

  const approved = await startVerification(request, '+15550003001');
  const badge = row(page, approved.code).locator('.verification');
  await expect(badge).toHaveText('sms · pending');

  const check = await request.post(`/v2/Services/${service}/VerificationCheck`,
    { form: { VerificationSid: approved.sid, Code: approved.code } });
  expect((await check.json()).status).toBe('approved');
  await expect(badge).toHaveText('sms · approved');

  const expired = await startVerification(request, '+15550003002');
  await openThread(page, '+15550003002');
  const expiredBadge = row(page, expired.code).locator('.verification');
  await expect(expiredBadge).toHaveText('sms · pending');
  expect((await request.post(`/api/v1/verifications/${expired.sid}/expire`)).status()).toBe(200);
  await expect(expiredBadge).toHaveText('sms · expired');

  // The first verification is still shown as approved.
  await openThread(page, '+15550003001');
  await expect(row(page, approved.code).locator('.verification')).toHaveText('sms · approved');
  expect(await page.evaluate(() => window.notReloaded)).toBe(true);
});

test('a pending verification shows expired once its expiry passes, with no event', async ({ page, request }) => {
  await page.clock.install();
  await page.goto('/');
  const v = await startVerification(request, '+15550003003');
  const badge = row(page, v.code).locator('.verification');
  await expect(badge).toHaveText('sms · pending');

  // Past the 10-minute expiry in the page only: the server still stores it as
  // pending, so no verification event is sent.
  await page.clock.fastForward('10:01');
  await expect(badge).toHaveText('sms · expired');
});

test('a badge expiring keeps keyboard focus where it was', async ({ page, request }) => {
  await page.clock.install();
  await page.goto('/');
  const v = await startVerification(request, '+15550003006');
  const badge = row(page, v.code).locator('.verification');
  await expect(badge).toHaveText('sms · pending');
  const copy = row(page, v.code).getByRole('button', { name: v.code });
  await copy.focus();

  await page.clock.fastForward('10:01');
  await expect(badge).toHaveText('sms · expired');
  await expect(copy).toBeFocused();
});

test('a change made before the event stream connects is still shown', async ({ page, request }) => {
  // Hold the page's event stream until the change below has been made, so the
  // change lands after the page's first read and before it subscribes.
  let release;
  const held = new Promise(resolve => { release = resolve; });
  await page.route('**/api/v1/events', async route => { await held; await route.continue(); });
  await page.goto('/');
  await expect(page.locator('#thread .empty')).toBeVisible();

  const v = await startVerification(request, '+15550003007');
  release();
  await expect(row(page, v.code).locator('.verification')).toHaveText('sms · pending');
});

test('a message with no verification has no badge', async ({ page, request }) => {
  const sent = await request.post('/api/v1/send', { data: { to: '+15550003004', body: 'Plain message 640012' } });
  expect(sent.status()).toBe(201);
  await page.goto('/');
  await expect(row(page, 'Plain message 640012')).toBeVisible();
  await expect(row(page, 'Plain message 640012').locator('.verification')).toHaveCount(0);
});

for (const colorScheme of ['light', 'dark']) {
  test(`the inbox with a badge has no serious or critical axe violations (${colorScheme})`, async ({ page, request }) => {
    await page.emulateMedia({ colorScheme });
    await page.goto('/');
    const v = await startVerification(request, '+15550003005');
    const badge = row(page, v.code).locator('.verification');
    // The status is in the badge's text, so it does not rely on colour.
    await expect(badge).toHaveText('sms · pending');

    const { violations } = await new AxeBuilder({ page }).analyze();
    const blocking = violations
      .filter(v => v.impact === 'serious' || v.impact === 'critical')
      .map(v => ({ id: v.id, impact: v.impact, nodes: v.nodes.map(n => n.target.join(' ')) }));
    expect(blocking).toEqual([]);
  });
}
