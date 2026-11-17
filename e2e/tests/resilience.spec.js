// 내 상태 카드 Island의 오류·순서 처리.
// 네트워크 실패와 응답 순서는 Playwright 경로 가로채기로 재현한다. 서버 동작(401·403·409·500)은 가능한 한 실제 서버 응답을 쓴다.
const { test, expect } = require('@playwright/test');
const { ObjectId } = require('mongodb');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

const ME = '**/api/me';
const CHECK_IN = '**/api/me/check-in';

test('조회 연결이 끊기면 마지막 상태를 유지하고 체크인을 막으며, 복구되면 다시 갱신한다', async ({ page }) => {
  await h.join(page, { name: '연결', email: 'net@example.com', size: 2 });
  await h.expectPhase(page, 'ready');

  await page.route(ME, (route) => route.abort('failed'));
  await expect(page.locator('#conn')).toHaveText('연결이 불안정합니다. 다시 연결하는 중입니다. 연결되면 체크인할 수 있습니다.');
  await expect(page.getByRole('button', { name: '체크인' })).toBeDisabled();
  await h.expectPhase(page, 'ready');
  await expect(page.getByText('연결 · 2명')).toBeVisible();

  await page.unroute(ME);
  await expect(page.locator('#conn')).toBeHidden({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: '체크인' })).toBeEnabled();
});

test('조회 500은 완료나 미등록으로 보이지 않는다', async ({ page }) => {
  await h.join(page, { name: '오류', email: 'e500@example.com', size: 2 });
  await h.expectPhase(page, 'ready');
  await page.route(ME, (route) => route.fulfill({
    status: 500, contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'internal_error', message: '일시적인 오류가 발생했습니다.' } }),
  }));
  await expect(page.locator('#conn')).toContainText('일시적인 오류로 최신 상태를 불러오지 못했습니다');
  await h.expectPhase(page, 'ready');
  await expect(page.getByRole('heading', { name: '이용이 완료되었습니다' })).toBeHidden();
  await expect(page.getByRole('heading', { name: '등록 정보를 확인할 수 없습니다' })).toBeHidden();
  await page.unroute(ME);
  await expect(page.locator('#conn')).toBeHidden({ timeout: 10_000 });
});

test('쿠키가 사라지면(401) 등록 정보 확인 불가 안내와 등록 이동 수단을 보여 준다', async ({ page, context }) => {
  await h.join(page, { name: '쿠키', email: 'c401@example.com', size: 2 });
  await h.expectPhase(page, 'ready');
  await context.clearCookies();
  await h.expectPhase(page, 'unregistered');
  await expect(page.getByRole('heading', { name: '등록 정보를 확인할 수 없습니다' })).toBeVisible();
  await page.getByRole('link', { name: '대기 등록하기' }).click();
  await expect(page).toHaveURL(/\/join$/);
});

test('끊어진 successor 참조는 미등록이 아니라 오류로 보인다', async ({ page }) => {
  await h.join(page, { name: '참조', email: 'broken@example.com', size: 2 });
  await h.updateParty('broken@example.com', { status: 'completed', successorId: new ObjectId() });

  const res = await page.goto('/status');
  expect(res.status()).toBe(500);
  await expect(page.getByRole('heading', { name: '잠시 후 다시 시도해 주세요' })).toBeVisible();
  await expect(page.getByText('등록 정보를 확인할 수 없습니다')).toHaveCount(0);

  const joinRes = await page.goto('/join');
  expect(joinRes.status()).toBe(500);
  await expect(page.locator('#join-form')).toHaveCount(0);
});

test('체크인 409(좌석 부족)는 사유를 안내하고 최신 상태로 돌아간 뒤, 좌석이 생기면 다시 체크인할 수 있다', async ({ page }) => {
  await h.join(page, { name: '좌석', email: 'seat@example.com', size: 3 });
  await h.expectPhase(page, 'ready');
  // 버튼을 누른 순간 다른 팀들이 좌석을 차지한 상황: 요청이 서버에 닿기 직전에 좌석을 채운다.
  await page.route(CHECK_IN, async (route) => { await h.setSeats(9); await route.continue(); });
  await page.getByRole('button', { name: '체크인' }).click();
  await expect(page.locator('#notice')).toHaveText('아직 빈 좌석이 부족합니다. 잠시 후 다시 확인해 주세요.');
  await h.expectPhase(page, 'waiting');
  await expect(page.locator('#notice')).toBeVisible();
  await page.unroute(CHECK_IN);

  await h.setSeats(0);
  await h.expectPhase(page, 'ready');
  await expect(page.locator('#notice')).toBeHidden();
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
});

test('체크인 403은 새로고침을 안내한다', async ({ page, playwright, baseURL }) => {
  await h.join(page, { name: '보안', email: 'c403@example.com', size: 2 });
  await h.expectPhase(page, 'ready');
  // 서버가 실제로 돌려준 CSRF 거부 응답을 브라우저 요청의 응답으로 전달한다. (요청은 서버에 닿지 않음)
  const api = await playwright.request.newContext({ baseURL });
  const forbidden = await h.realForbidden(api, '/api/me/check-in');
  await page.route(CHECK_IN, (route) => route.fulfill({ response: forbidden }));
  await page.getByRole('button', { name: '체크인' }).click();
  await expect(page.locator('#notice')).toHaveText('요청이 거부되었습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.');
  await h.expectPhase(page, 'ready');
  expect((await h.partyByEmail('c403@example.com')).status).toBe('waiting');
  await api.dispose();
});

test('체크인 응답이 유실돼도 실패로 단정하지 않고 조회로 실제 처리 여부를 확인한다', async ({ page }) => {
  await h.join(page, { name: '유실', email: 'lost@example.com', size: 2 });
  await h.expectPhase(page, 'ready');
  // 서버는 체크인을 처리했지만 응답이 브라우저에 도착하지 않음
  await page.route(CHECK_IN, async (route) => { await route.fetch(); await route.abort('failed'); });
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining', { timeout: 5000 });
  await expect(page.locator('#notice')).toBeHidden();
  expect((await h.partyByEmail('lost@example.com')).status).toBe('dining');
});

test('체크인 요청이 서버에 닿지 않았으면 다시 시도하도록 안내한다', async ({ page }) => {
  await h.join(page, { name: '미전송', email: 'unsent@example.com', size: 2 });
  await h.expectPhase(page, 'ready');
  await page.route(CHECK_IN, (route) => route.abort('failed'));
  await page.getByRole('button', { name: '체크인' }).click();
  await expect(page.locator('#notice')).toHaveText('체크인이 처리되지 않았습니다. 다시 시도해 주세요.');
  await h.expectPhase(page, 'ready');
  await page.unroute(CHECK_IN);
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
});

test('체크인 전에 보낸 조회의 늦은 응답이 체크인 결과를 덮어쓰지 않는다', async ({ page }) => {
  await h.join(page, { name: '순서', email: 'order@example.com', size: 2 });
  await h.expectPhase(page, 'ready');

  // 다음 조회 하나를 붙잡아 둔다: 서버 응답(ready)은 바로 받아 두고, 체크인이 끝난 뒤에 브라우저로 보낸다.
  let release;
  const released = new Promise((r) => { release = r; });
  let held;
  const heldReady = new Promise((r) => { held = r; });
  let holding = true;
  await page.route(ME, async (route) => {
    if (!holding) return route.continue();
    holding = false;
    const stale = await route.fetch();
    held();
    await released;
    await route.fulfill({ response: stale }).catch(() => {}); // 브라우저가 이미 취소했으면 무시
  });
  await heldReady; // 조회 하나가 진행 중인 상태
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
  release();
  await page.waitForTimeout(1500);
  await h.expectPhase(page, 'dining');
  await page.unroute(ME);

  // 같은 팀의 이전 단계(대기) 응답이 뒤늦게 와도 되돌아가지 않는다.
  const registeredAt = await page.evaluate(() => JSON.parse(document.getElementById('initial-state').textContent).party.registeredAt);
  await page.route(ME, (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ party: { name: '순서', partySize: 2, status: 'waiting', registeredAt }, waiting: { aheadCount: 0, canCheckIn: true } }),
  }));
  await page.waitForTimeout(2500);
  await h.expectPhase(page, 'dining');
  await page.unroute(ME);
});

test('카운트다운이 0이 되어도 서버가 완료하기 전에는 완료로 바꾸지 않는다', async ({ page }) => {
  await h.join(page, { name: '시간', email: 'clock@example.com', size: 1 });
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');

  // 서버가 아직 식사 중이라고 답하는 상황(종료 작업이 처리하기 전): 종료 예정 시각이 이미 지난 식사 중 응답
  const registeredAt = await page.evaluate(() => JSON.parse(document.getElementById('initial-state').textContent).party.registeredAt);
  await page.route(ME, (route) => {
    const now = new Date();
    return route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({
        party: { name: '시간', partySize: 1, status: 'dining', registeredAt },
        dining: { serviceEndsAt: new Date(now.getTime() - 1000).toISOString(), serverTime: now.toISOString() },
      }),
    });
  });
  await expect(page.locator('[data-field="remaining"]')).toHaveText('이용 완료 확인 중');
  await page.waitForTimeout(2000);
  await h.expectPhase(page, 'dining');

  // 실제 서버 응답으로 돌아가면 완료 화면이 된다.
  await page.unroute(ME);
  await h.expectPhase(page, 'completed', { timeout: 10_000 });
});
