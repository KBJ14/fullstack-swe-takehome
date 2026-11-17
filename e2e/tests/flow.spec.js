// 실제 API·DB·식사 종료 작업으로 전체 흐름을 확인한다.
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

test('등록부터 체크인, 식사 완료, 재등록까지', async ({ page }) => {
  await h.join(page, { name: '이용객', email: 'flow@example.com', size: 1 });
  await h.expectPhase(page, 'ready');
  await expect(page.getByText('이용객 · 1명')).toBeVisible();

  // 진행 중인 등록이 있으면 등록 화면 대신 상태 화면으로 안내한다.
  await page.goto('/join');
  await expect(page).toHaveURL(/\/status$/);

  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
  await expect(page.getByRole('heading', { name: '식사 중입니다' })).toBeVisible();
  await expect(page.locator('[data-field="remaining"]')).toHaveText(/^[1-3]초$/);

  // 1명 × 3초 뒤 서버의 식사 종료 작업이 완료 처리한다.
  await h.expectPhase(page, 'completed', { timeout: 15_000 });
  await expect(page.getByRole('heading', { name: '이용이 완료되었습니다' })).toBeVisible();

  // 완료 상태에서는 반복 조회를 멈춘다.
  const polls = h.countPolls(page);
  await page.waitForTimeout(2500);
  expect(polls.n).toBe(0);

  // 완료 후 /join은 새 등록 폼을 보여 준다.
  await page.getByRole('link', { name: '다시 대기 등록하기' }).click();
  await expect(page).toHaveURL(/\/join$/);
  await expect(page.getByLabel('이름')).toBeVisible();

  // 같은 이메일로 재등록
  await page.getByLabel('이름').fill('이용객');
  await page.getByLabel('이메일').fill('FLOW@example.com');
  await page.getByLabel('인원수').fill('2');
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page).toHaveURL(/\/status$/);
  await h.expectPhase(page, 'ready');
  await expect(page.getByText('이용객 · 2명')).toBeVisible();
});
