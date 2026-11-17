// E2E 공용 도구.
//
// DB 도구는 E2E 전용 임시 DB(E2E_DATABASE, 이름이 e2e_로 시작)만 다룬다.
// 정상 흐름은 실제 화면·API를 쓰고, DB 직접 변경은 테스트 사이 초기화와
// 화면으로 만들 수 없는 상황(좌석 강제 부족, 끊어진 참조) 재현에만 쓴다.
const { MongoClient } = require('mongodb');
const { expect } = require('@playwright/test');

const dbName = process.env.E2E_DATABASE || '';
if (!/^e2e_[A-Za-z0-9_]+$/.test(dbName)) {
  throw new Error(`E2E_DATABASE must start with "e2e_" (got "${dbName}"); refusing to touch other databases`);
}

let client;
async function db() {
  if (!client) {
    client = new MongoClient(process.env.MONGODB_URI || 'mongodb://mongo:27017/?replicaSet=rs0');
    await client.connect();
  }
  return client.db(dbName);
}

async function closeDb() {
  if (client) await client.close();
  client = undefined;
}

// 테스트 사이 초기화: 이 임시 DB의 팀을 지우고 좌석·순번을 처음으로 되돌린다.
async function resetDb() {
  const d = await db();
  await d.collection('parties').deleteMany({});
  await d.collection('restaurants').updateOne({ _id: 'default' }, { $set: { seatsInUse: 0, nextSequence: 0 } });
}

async function setSeats(n) {
  await (await db()).collection('restaurants').updateOne({ _id: 'default' }, { $set: { seatsInUse: n } });
}

async function partyByEmail(email) {
  return (await db()).collection('parties').findOne({ emailNormalized: email.trim().toLowerCase() }, { sort: { sequence: -1 } });
}

async function updateParty(email, set) {
  const p = await partyByEmail(email);
  await (await db()).collection('parties').updateOne({ _id: p._id }, { $set: set });
}

// 화면에서 등록 폼을 채워 제출한다.
async function join(page, { name, email, size }) {
  await page.goto('/join');
  await page.getByLabel('이름').fill(name);
  await page.getByLabel('이메일').fill(email);
  await page.getByLabel('인원수').fill(String(size));
  await page.getByRole('button', { name: '대기 등록하기' }).click();
  await expect(page).toHaveURL(/\/status$/);
}

// 내 상태 카드의 단계 (data-phase)
function card(page) {
  return page.locator('#status-card');
}

async function expectPhase(page, phase, opts) {
  await expect(card(page)).toHaveAttribute('data-phase', phase, opts);
}

// 탭 숨김·복귀를 흉내 낸다. (헤드리스 브라우저는 실제로 탭을 숨길 수 없음)
async function setHidden(page, hidden) {
  await page.evaluate((h) => {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (h ? 'hidden' : 'visible') });
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => h });
    document.dispatchEvent(new Event('visibilitychange'));
  }, hidden);
}

// GET /api/me 요청 수를 센다.
function countPolls(page) {
  const state = { n: 0 };
  page.on('request', (req) => {
    if (req.method() === 'GET' && new URL(req.url()).pathname === '/api/me') state.n += 1;
  });
  return state;
}

// 서버의 실제 CSRF 거부(403) 응답을 받아 온다.
// 브라우저는 Sec-Fetch-Site 헤더를 스크립트나 가로채기로 바꾸지 못하게 하므로,
// API 요청으로 다른 출처 요청을 만들어 실제 서버 응답을 얻고, 그것을 브라우저 요청의 응답으로 전달한다.
async function realForbidden(request, path) {
  const res = await request.post(path, {
    headers: { 'Sec-Fetch-Site': 'cross-site', 'Content-Type': 'application/json' },
    data: path === '/api/parties' ? { name: 'x', email: 'x@example.com', partySize: 1 } : undefined,
  });
  expect(res.status()).toBe(403);
  return res;
}

module.exports = {
  db, closeDb, resetDb, setSeats, partyByEmail, updateParty, join, card, expectPhase, setHidden, countPolls, realForbidden,
};
