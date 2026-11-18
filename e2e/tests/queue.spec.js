// 여러 브라우저의 선착순·앞 팀 수·좌석 반환을 실제 API·DB·식사 종료 작업으로 확인한다.
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

test('두 브라우저에서 선착순과 앞 팀 수가 자동으로 갱신된다', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage();
  const b = await (await browser.newContext()).newPage();
  await h.join(a, { name: '앞 손님', email: 'a@example.com', size: 2 });
  await h.join(b, { name: '뒤 손님', email: 'b@example.com', size: 3 });

  await h.expectPhase(a, 'ready');
  await h.expectPhase(b, 'waiting');
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
  await expect(b.getByRole('button', { name: '체크인' })).toBeHidden();

  // 서버가 그린 문구와 자동 갱신 후 문구가 같다.
  const firstPoll = b.waitForResponse((r) => new URL(r.url()).pathname === '/api/me' && r.status() === 200);
  await b.reload();
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
  await firstPoll; // 첫 자동 갱신 응답이 반영된 뒤에도 같은 문구
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');

  // 앞 팀이 체크인하면 뒤 팀 화면이 새로고침 없이 바뀐다. (빈 좌석 8 ≥ 3)
  await a.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(a, 'dining');
  await h.expectPhase(b, 'ready', { timeout: 5000 });
  await expect(b.getByRole('button', { name: '체크인' })).toBeEnabled();
});

// 세 손님(쿠키가 분리된 브라우저 컨텍스트 3개)이 실제 화면·API·DB·식사 종료 작업으로 이용한다. 응답 모킹 없음.
//   A 6명 체크인 → 빈 좌석 4 → B 5명, C 2명 순서로 등록
//   B는 좌석 부족으로 대기, C는 4석에 들어갈 수 있어도 B를 추월하지 못함
//   A 식사 종료(18초) → 새로고침 없이 B에 체크인 버튼 → B 체크인 → C의 앞 팀 수·체크인 가능이 실제 좌석(빈 5석)에 맞게 갱신
test('맨 앞 팀이 들어갈 수 없으면 작은 뒤 팀도 기다리고, 좌석이 반환되면 순서대로 체크인 버튼이 나타난다', async ({ browser }) => {
  test.setTimeout(90_000);
  const a = await (await browser.newContext()).newPage();
  const bctx = await browser.newContext();
  const b = await bctx.newPage();
  const cctx = await browser.newContext();
  const c = await cctx.newPage();

  await h.join(a, { name: '여섯 명', email: 'six@example.com', size: 6 });
  await a.getByRole('button', { name: '체크인' }).click(); // 6명 식사 시작, 18초
  await h.expectPhase(a, 'dining');

  await h.join(b, { name: '다섯 명', email: 'five@example.com', size: 5 });
  await h.join(c, { name: '두 명', email: 'two@example.com', size: 2 });

  // 빈 좌석 4: 맨 앞 B(5명)는 기다린다.
  await h.expectPhase(b, 'waiting');
  await expect(b.locator('[data-field="ahead"]')).toHaveText('다음 순서입니다');
  await expect(b.locator('[data-field="hint"]')).toHaveText('빈 좌석이 생기면 체크인 버튼이 나타납니다.');
  await expect(b.getByRole('button', { name: '체크인' })).toBeHidden();

  // C(2명)는 4석에 들어갈 수 있지만 B를 추월하지 못한다: 화면에 버튼이 없고, 서버도 거부한다.
  await h.expectPhase(c, 'waiting');
  await expect(c.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
  await expect(c.getByRole('button', { name: '체크인' })).toBeHidden();
  const direct = await cctx.request.post('/api/me/check-in'); // 같은 브라우저 쿠키로 직접 요청
  expect(direct.status()).toBe(409);
  expect((await direct.json()).error.code).toBe('not_your_turn');
  expect((await (await h.db()).collection('restaurants').findOne({ _id: 'default' })).seatsInUse).toBe(6);

  // A의 식사가 끝나 좌석이 반환되면 새로고침 없이 B 화면에 체크인 버튼이 나타난다. C는 계속 대기.
  await h.expectPhase(a, 'completed', { timeout: 30_000 });
  await h.expectPhase(b, 'ready', { timeout: 5000 });
  await expect(b.getByRole('button', { name: '체크인' })).toBeEnabled();
  await h.expectPhase(c, 'waiting');
  await expect(c.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');

  // B가 체크인하면 C는 맨 앞이 되고, 빈 좌석 5 ≥ 2이므로 체크인 버튼이 나타난다.
  await b.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(b, 'dining');
  await h.expectPhase(c, 'ready', { timeout: 5000 });
  await expect(c.getByRole('button', { name: '체크인' })).toBeEnabled();
  expect((await (await h.db()).collection('restaurants').findOne({ _id: 'default' })).seatsInUse).toBe(5);

  await c.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(c, 'dining');
  expect((await (await h.db()).collection('restaurants').findOne({ _id: 'default' })).seatsInUse).toBe(7);
});
