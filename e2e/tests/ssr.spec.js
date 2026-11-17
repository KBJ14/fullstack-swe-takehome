// 서버가 실제 내용이 있는 HTML을 보내는지 확인한다. (SSR)
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

test('초기 HTML에 등록 폼이 들어 있다', async ({ request }) => {
  const res = await request.get('/join');
  expect(res.status()).toBe(200);
  expect(res.headers()['cache-control']).toBe('no-store');
  const html = await res.text();
  for (const s of [
    '<label for="name">이름</label>', '<label for="email">이메일</label>', '<label for="partySize">인원수</label>',
    'id="join-form"', 'type="email"', 'min="1" max="10" step="1"', '<noscript>',
  ]) expect(html).toContain(s);
});

test('초기 HTML에 내 상태가 들어 있다', async ({ request }) => {
  // request 컨텍스트는 쿠키를 유지한다. (브라우저가 아닌 요청이므로 CSRF 검사에서 허용됨)
  const r = await request.post('/api/parties', { data: { name: '김하나', email: 'ssr@example.com', partySize: 2 } });
  expect(r.status()).toBe(201);

  const res = await request.get('/status');
  expect(res.status()).toBe(200);
  expect(res.headers()['cache-control']).toBe('no-store');
  const html = await res.text();
  expect(html).toContain('data-phase="ready"');
  expect(html).toContain('김하나 · 2명');
  expect(html).toContain('입장 차례입니다');
  expect(html).toContain('id="initial-state">{"party":{"name":"김하나"');
});

test('미등록 상태 페이지는 안내와 등록 이동 수단을 담는다', async ({ request }) => {
  const html = await (await request.get('/status')).text();
  expect(html).toContain('data-phase="unregistered"');
  expect(html).toContain('등록 정보를 확인할 수 없습니다');
  expect(html).toContain('href="/join"');
});

test('JavaScript 안내 문구가 HTML에 들어 있다', async ({ request }) => {
  // Playwright의 javaScriptEnabled:false는 스크립트 실행만 막는 에뮬레이션이라 <noscript> 내용을 그리지 않는다.
  // 따라서 안내 문구는 HTML 수준에서 확인한다.
  expect(await (await request.get('/join')).text()).toMatch(/<noscript>\s*<p class="noscript">대기 등록에는 JavaScript가 필요합니다/);
  expect(await (await request.get('/status')).text()).toMatch(/<noscript>\s*<p class="noscript">자동 갱신과 체크인에는 JavaScript가 필요합니다/);
});

test('JavaScript가 꺼져 있어도 내용이 보이고, 동작 버튼은 눌리지 않는다', async ({ browser }) => {
  const ctx = await browser.newContext({ javaScriptEnabled: false });
  const page = await ctx.newPage();

  await page.goto('/join');
  await expect(page.getByLabel('이름')).toBeVisible();
  await expect(page.getByRole('button', { name: '대기 등록하기' })).toBeDisabled();

  // 같은 컨텍스트의 request는 브라우저 쿠키를 공유한다.
  expect((await ctx.request.post('/api/parties', { data: { name: '김둘', email: 'nojs@example.com', partySize: 3 } })).status()).toBe(201);
  await page.goto('/status');
  await expect(page.getByText('김둘 · 3명')).toBeVisible();
  await expect(page.getByRole('heading', { name: '입장 차례입니다' })).toBeVisible();
  await expect(page.getByRole('button', { name: '체크인' })).toBeDisabled();
  await ctx.close();
});

test('첫 화면은 접속 상태에 따라 등록 또는 상태 화면으로 안내한다', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveURL(/\/join$/);
  await h.join(page, { name: '안내', email: 'root@example.com', size: 2 });
  await page.goto('/');
  await expect(page).toHaveURL(/\/status$/);
});
