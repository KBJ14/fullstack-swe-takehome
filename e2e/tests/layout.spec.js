// 모바일·데스크톱 화면 배치. desktop과 mobile(Pixel 7 에뮬레이션) 두 프로젝트에서 실행된다.
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

async function checkLayout(page, testInfo, name) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0); // 가로 스크롤 없음
  for (const btn of await page.locator('.btn:visible').all()) {
    const box = await btn.boundingBox();
    expect(box.height).toBeGreaterThanOrEqual(44); // 터치 영역
  }
  await page.screenshot({ path: testInfo.outputPath(`${name}.png`), fullPage: true });
}

test('등록·상태 화면이 화면 너비에 맞고 터치하기 쉽다', async ({ page }, testInfo) => {
  await page.goto('/join');
  const inputFont = await page.getByLabel('이름').evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(inputFont).toBeGreaterThanOrEqual(16); // 모바일 확대 방지·가독성
  await checkLayout(page, testInfo, 'join');

  await h.join(page, { name: '아주아주긴이름을가진손님'.repeat(4), email: 'layout@example.com', size: 4 });
  await h.expectPhase(page, 'ready');
  await checkLayout(page, testInfo, 'status-ready');

  await page.getByRole('button', { name: '체크인' }).click();
  await h.expectPhase(page, 'dining');
  await checkLayout(page, testInfo, 'status-dining');
});
