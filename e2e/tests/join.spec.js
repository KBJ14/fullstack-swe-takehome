// 등록 폼 Island: 입력 오류, 중복, 403·연결 실패·서버 오류, 중복 제출 방지
const { test, expect } = require('@playwright/test');
const h = require('../helpers');

test.beforeEach(h.resetDb);
test.afterAll(h.closeDb);

async function fill(page, name, email, size) {
  await page.getByLabel('이름').fill(name);
  await page.getByLabel('이메일').fill(email);
  await page.getByLabel('인원수').fill(String(size));
}

test('브라우저 입력 검사: 빈 값과 범위를 서버 요청 없이 안내한다', async ({ page }) => {
  let posts = 0;
  page.on('request', (r) => { if (r.method() === 'POST') posts += 1; });
  await page.goto('/join');
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page.locator('#name-error')).toHaveText('이름을 입력해 주세요.');
  await expect(page.locator('#email-error')).toHaveText('이메일을 입력해 주세요.');
  await expect(page.locator('#partySize-error')).toHaveText('인원수를 입력해 주세요.');
  await expect(page.getByLabel('이름')).toBeFocused();

  await fill(page, '홍길동', 'hong@example.com', 11);
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page.locator('#partySize-error')).toHaveText('인원수는 1~10 사이의 정수로 입력해 주세요.');
  expect(posts).toBe(0);
});

test('서버 검증 오류(400)를 필드 옆에 보여 주고 입력값을 유지한다', async ({ page }) => {
  await page.goto('/join');
  // 브라우저는 a@b를 이메일로 허용하지만 서버는 도메인에 점이 없으면 거부한다.
  await fill(page, '홍길동', 'a@b', 2);
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page.locator('#email-error')).toHaveText('올바른 이메일 형식이 아닙니다.');
  await expect(page.locator('#form-error')).toHaveText('입력값을 확인해 주세요.');
  await expect(page.getByLabel('이메일')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByLabel('이메일')).toBeFocused();
  await expect(page).toHaveURL(/\/join$/);
  await expect(page.getByLabel('이름')).toHaveValue('홍길동');
  await expect(page.getByLabel('인원수')).toHaveValue('2');

  // 고쳐서 다시 제출하면 등록된다.
  await page.getByLabel('이메일').fill('hong@example.com');
  await expect(page.locator('#email-error')).toBeHidden();
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page).toHaveURL(/\/status$/);
});

test('진행 중인 같은 이메일(409)을 안내한다', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage();
  await h.join(a, { name: '첫 손님', email: 'dup@example.com', size: 2 });

  const b = await (await browser.newContext()).newPage();
  await b.goto('/join');
  await fill(b, '둘째 손님', ' DUP@example.com ', 3);
  await b.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(b.locator('#email-error')).toHaveText('이 이메일로 진행 중인 등록이 있습니다. 등록했던 브라우저에서 확인해 주세요.');
  await expect(b).toHaveURL(/\/join$/);
  // 다른 팀의 정보는 보이지 않는다.
  await expect(b.getByText('첫 손님')).toHaveCount(0);
});

test('403·연결 실패·서버 오류를 안내하고 같은 입력으로 다시 시도할 수 있다', async ({ page, playwright, baseURL }) => {
  await page.goto('/join');
  await fill(page, '재시도', 'retry@example.com', 2);
  const submit = page.getByRole('button', { name: '대기 등록하기' });

  // 403: 서버가 실제로 돌려준 CSRF 거부 응답을 브라우저 요청의 응답으로 전달한다.
  const api = await playwright.request.newContext({ baseURL });
  const forbidden = await h.realForbidden(api, '/api/parties');
  await page.route('**/api/parties', (route) => route.fulfill({ response: forbidden }));
  await submit.click();
  await expect(page.locator('#form-error')).toHaveText('보안 확인에 실패했습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.');
  await page.unroute('**/api/parties');
  await api.dispose();

  // 연결 실패
  await page.route('**/api/parties', (route) => route.abort('failed'));
  await submit.click();
  await expect(page.locator('#form-error')).toHaveText('서버에 연결할 수 없습니다. 네트워크 연결을 확인한 뒤 다시 시도해 주세요.');
  await page.unroute('**/api/parties');

  // 서버 오류 (응답을 흉내 냄)
  await page.route('**/api/parties', (route) => route.fulfill({
    status: 500, contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'internal_error', message: '일시적인 오류가 발생했습니다.' } }),
  }));
  await submit.click();
  await expect(page.locator('#form-error')).toHaveText('일시적인 오류로 등록하지 못했습니다. 잠시 후 다시 시도해 주세요.');
  await page.unroute('**/api/parties');

  // 입력값이 유지되어 그대로 다시 제출하면 등록된다.
  await expect(page.getByLabel('이름')).toHaveValue('재시도');
  await expect(submit).toBeEnabled();
  await submit.click();
  await expect(page).toHaveURL(/\/status$/);
});

test('처리 중에는 버튼이 비활성화되고 중복 제출되지 않는다', async ({ page }) => {
  let posts = 0;
  await page.route('**/api/parties', async (route) => {
    posts += 1;
    await new Promise((r) => setTimeout(r, 800));
    await route.continue();
  });
  await page.goto('/join');
  await fill(page, '한번만', 'once@example.com', 2);
  const submit = page.getByRole('button', { name: '대기 등록하기' });
  await submit.click();
  await expect(page.getByRole('button', { name: '등록하는 중…' })).toBeDisabled();
  await page.getByLabel('인원수').press('Enter');
  await page.getByLabel('이름').press('Enter');
  await expect(page).toHaveURL(/\/status$/);
  expect(posts).toBe(1);
  expect(await (await h.db()).collection('parties').countDocuments()).toBe(1);
});
