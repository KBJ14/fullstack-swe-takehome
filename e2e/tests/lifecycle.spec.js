// 탭 숨김·복귀, 뒤로가기·앞으로가기, 브라우저 종료 후 재실행
const fs = require('fs');
const os = require('os');
const path = require('path');
const { test, expect, chromium } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

test('탭이 숨겨지면 조회를 멈추고, 다시 보이면 즉시 최신 상태를 확인한다', async ({ browser }) => {
  const actx = await browser.newContext();
  const a = await actx.newPage();
  const b = await (await browser.newContext()).newPage();
  await h.join(a, { name: '앞', email: 'va@example.com', size: 2 });
  await h.join(b, { name: '뒤', email: 'vb@example.com', size: 2 });
  await h.expectPhase(b, 'waiting');

  const polls = h.countPolls(b);
  await h.setHidden(b, true);
  await b.waitForTimeout(300);
  const before = polls.n;
  // 숨겨진 동안 앞 팀이 체크인한다. (화면 대신 같은 브라우저 컨텍스트의 API 요청)
  expect((await actx.request.post('/api/me/check-in')).status()).toBe(200);
  await b.waitForTimeout(3000);
  expect(polls.n - before).toBe(0);
  await h.expectPhase(b, 'waiting'); // 숨겨진 동안에는 갱신되지 않음

  await h.setHidden(b, false);
  await h.expectPhase(b, 'ready', { timeout: 1000 }); // 다시 보이면 바로 확인
  expect(polls.n).toBeGreaterThan(before);
});

test('뒤로가기·앞으로가기 후에도 최신 상태를 확인하고 갱신을 이어 간다', async ({ browser }) => {
  const actx = await browser.newContext();
  const a = await actx.newPage();
  const b = await (await browser.newContext()).newPage();
  await h.join(a, { name: '앞', email: 'ba@example.com', size: 2 });
  await h.join(b, { name: '뒤', email: 'bb@example.com', size: 2 });
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');

  await b.goto('about:blank');
  expect((await actx.request.post('/api/me/check-in')).status()).toBe(200);
  await b.goBack();
  await expect(b).toHaveURL(/\/status$/);
  await h.expectPhase(b, 'ready', { timeout: 5000 });

  // 조회가 계속된다.
  const polls = h.countPolls(b);
  await b.waitForTimeout(2500);
  expect(polls.n).toBeGreaterThanOrEqual(2);

  await b.goForward();
  await b.goBack();
  await h.expectPhase(b, 'ready', { timeout: 5000 });
  const again = h.countPolls(b);
  await b.waitForTimeout(2500);
  expect(again.n).toBeGreaterThanOrEqual(2);
});

test('브라우저를 완전히 종료한 뒤 같은 프로필로 다시 열면 상태가 복원된다', async ({ baseURL }) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'waitlist-profile-'));
  try {
    let ctx = await chromium.launchPersistentContext(dir, { baseURL, locale: 'ko-KR' });
    let page = ctx.pages()[0] || (await ctx.newPage());
    await h.join(page, { name: '재방문', email: 'restart@example.com', size: 3 });
    await h.expectPhase(page, 'ready');
    await ctx.close(); // 브라우저 프로세스 종료

    ctx = await chromium.launchPersistentContext(dir, { baseURL, locale: 'ko-KR' });
    page = ctx.pages()[0] || (await ctx.newPage());
    await page.goto('/');
    await expect(page).toHaveURL(/\/status$/);
    await expect(page.getByText('재방문 · 3명')).toBeVisible();
    await h.expectPhase(page, 'ready');
    await ctx.close();

    // 대조군: 다른 프로필(쿠키 없음)에서는 복원되지 않는다.
    const other = await chromium.launchPersistentContext(fs.mkdtempSync(path.join(os.tmpdir(), 'waitlist-other-')), { baseURL });
    const op = other.pages()[0] || (await other.newPage());
    await op.goto('/status');
    await h.expectPhase(op, 'unregistered');
    await other.close();
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
