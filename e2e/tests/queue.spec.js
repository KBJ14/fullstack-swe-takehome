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
  await b.reload();
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
  await b.waitForTimeout(1500);
  await expect(b.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');

  // 앞 팀이 체크인하면 뒤 팀 화면이 새로고침 없이 바뀐다. (빈 좌석 8 ≥ 3)
  await a.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(a, 'dining');
  await h.expectPhase(b, 'ready', { timeout: 5000 });
  await expect(b.getByRole('button', { name: '체크인' })).toBeEnabled();
});

test('맨 앞 팀이 들어갈 수 없으면 작은 뒤 팀도 기다리고, 좌석이 반환되면 체크인 버튼이 나타난다', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage();
  const b = await (await browser.newContext()).newPage();
  const c = await (await browser.newContext()).newPage();
  await h.join(a, { name: '네 명', email: 'four@example.com', size: 4 });
  await h.join(b, { name: '일곱 명', email: 'seven@example.com', size: 7 });
  await h.join(c, { name: '한 명', email: 'one@example.com', size: 1 });

  await a.getByRole('button', { name: '체크인' }).click(); // 4명 식사 시작, 12초
  await h.expectPhase(a, 'dining');

  // 빈 좌석 6: 맨 앞 7명은 기다리고, 1명 팀은 좌석이 충분해도 추월하지 못한다.
  await h.expectPhase(b, 'waiting');
  await expect(b.locator('[data-field="ahead"]')).toHaveText('다음 순서입니다');
  await expect(b.locator('[data-field="hint"]')).toHaveText('빈 좌석이 생기면 체크인 버튼이 나타납니다.');
  await expect(c.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
  await c.waitForTimeout(1500);
  await h.expectPhase(c, 'waiting');
  await expect(c.getByRole('button', { name: '체크인' })).toBeHidden();

  // 4명 팀의 식사가 끝나 좌석이 반환되면 7명 팀 화면에 체크인 버튼이 자동으로 나타난다.
  await h.expectPhase(a, 'completed', { timeout: 20_000 });
  await h.expectPhase(b, 'ready', { timeout: 5000 });
  await expect(b.getByRole('button', { name: '체크인' })).toBeEnabled();
  await h.expectPhase(c, 'waiting');
  await expect(c.locator('[data-field="ahead"]')).toHaveText('내 앞에 1팀');
});
