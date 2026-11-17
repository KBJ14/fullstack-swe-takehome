// 키보드 조작, 오류 연결, 입력 이스케이프, 스크린리더 알림 범위
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

async function tabUntil(page, selector, max = 8) {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press('Tab');
    if (await page.locator(selector).evaluate((el) => el === document.activeElement)) return;
  }
  throw new Error('could not reach ' + selector + ' with Tab');
}

test('키보드만으로 등록하고 체크인할 수 있으며 포커스가 보인다', async ({ page }) => {
  await page.goto('/join');
  await tabUntil(page, '#name');
  await page.keyboard.type('키보드');
  await page.keyboard.press('Tab');
  await expect(page.locator('#email')).toBeFocused();
  await page.keyboard.type('kbd@example.com');
  await page.keyboard.press('Tab');
  await expect(page.locator('#partySize')).toBeFocused();
  await page.keyboard.type('2');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: '대기 등록하기' })).toBeFocused();
  const outline = await page.locator('button[type="submit"]').evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe('none');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/status$/);

  await h.expectPhase(page, 'ready');
  await expect(page.getByRole('button', { name: '체크인' })).toBeEnabled();
  await tabUntil(page, '#check-in');
  await page.keyboard.press('Enter');
  await h.expectPhase(page, 'dining');
});

test('입력 오류는 필드와 연결되어 전달된다', async ({ page }) => {
  await page.goto('/join');
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  const name = page.getByLabel('이름');
  await expect(name).toHaveAttribute('aria-invalid', 'true');
  expect(await name.getAttribute('aria-describedby')).toContain('name-error');
  await expect(page.locator('#name-error')).toBeVisible();
  // 접근성 트리에서 오류 문구가 입력란의 설명으로 연결된다.
  await expect(name).toHaveAccessibleDescription(/이름을 입력해 주세요/);
});

test('사용자 입력은 문자 그대로 표시되고 실행되지 않는다', async ({ page }) => {
  const evil = '<img src=x onerror="window.__xss=1"><b>굵게</b>';
  await h.join(page, { name: evil, email: 'xss@example.com', size: 2 });
  const line = page.locator('[data-field="party"]');
  await expect(line).toHaveText(evil + ' · 2명'); // 서버 렌더링
  await page.waitForTimeout(1500); // 조회로 다시 그린 뒤에도
  await expect(line).toHaveText(evil + ' · 2명');
  expect(await page.locator('#status-card img, #status-card b').count()).toBe(0);
  expect(await page.evaluate(() => window.__xss)).toBeUndefined();
});

test('남은 시간은 매초 낭독되는 영역 밖에 있고, 상태 변화는 알림 영역으로 전달된다', async ({ page }) => {
  await h.join(page, { name: '낭독', email: 'sr@example.com', size: 2 });
  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
  const inLive = await page.locator('[data-field="remaining"]').evaluate((el) => !!el.closest('[aria-live], [role="status"], [role="alert"]'));
  expect(inLive).toBe(false);
  await expect(page.locator('#announce')).toHaveText('체크인되었습니다. 식사를 즐겨 주세요.');
});
