// 앱 프로세스 재시작 복구 검증 (scripts/e2e-restart.sh가 단계별로 호출).
//
//   setup       : A(2명) 등록·체크인, B(9명) 등록. A는 식사 중, B는 좌석 부족으로 대기. 상태를 JSON으로 출력
//   while-down  : (앱 강제 종료 상태) 앱에 접속할 수 없고, A의 종료 예정 시각이 지날 때까지 기다린 뒤에도
//                 DB에는 A가 식사 중·좌석 2로 그대로 남아 있음을 확인
//   after-start : (같은 DB로 앱 재시작) A가 완료되고 좌석이 정확히 반환되며, B가 체크인할 수 있고,
//                 이후 여러 순회가 지나도 좌석이 중복 반환되지 않음을 확인
//
// 실제 API(HTTP)와 실제 MongoDB만 사용한다. 응답 모킹 없음. 임시 DB(e2e_*)만 읽는다.
const { MongoClient } = require('mongodb');

const BASE = process.env.BASE_URL || 'http://app-e2e:8080';
const DB = process.env.E2E_DATABASE || '';
if (!/^e2e_[A-Za-z0-9_]+$/.test(DB)) throw new Error('E2E_DATABASE must start with e2e_');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function check(cond, msg, detail) {
  if (!cond) {
    console.error('FAIL:', msg, detail === undefined ? '' : JSON.stringify(detail));
    process.exit(1);
  }
  console.error('ok  :', msg);
}

async function waitFor(fn, timeoutMs, what) {
  const start = Date.now();
  for (;;) {
    const v = await fn().catch(() => undefined);
    if (v) return Date.now() - start;
    if (Date.now() - start > timeoutMs) throw new Error('timeout waiting for ' + what);
    await sleep(200);
  }
}

async function api(method, path, cookie, body) {
  const headers = { Accept: 'application/json' };
  if (cookie) headers.Cookie = cookie;
  if (body) headers['Content-Type'] = 'application/json';
  const res = await fetch(BASE + path, { method, headers, body: body ? JSON.stringify(body) : undefined });
  const setCookie = res.headers.get('set-cookie');
  return { status: res.status, json: await res.json().catch(() => null), cookie: setCookie ? setCookie.split(';')[0] : null };
}

async function withDb(fn) {
  const client = new MongoClient(process.env.MONGODB_URI || 'mongodb://mongo:27017/?replicaSet=rs0');
  await client.connect();
  try { return await fn(client.db(DB)); } finally { await client.close(); }
}
const seats = (db) => db.collection('restaurants').findOne({ _id: 'default' }).then((r) => r.seatsInUse);
const partyStatus = (db, email) => db.collection('parties').findOne({ emailNormalized: email }).then((p) => p.status);

async function setup() {
  await waitFor(async () => (await fetch(BASE + '/readyz')).status === 200, 60_000, 'app ready');
  const a = await api('POST', '/api/parties', null, { name: 'A', email: 'restart-a@example.com', partySize: 2 });
  check(a.status === 201 && a.cookie, 'A registered', a.status);
  const b = await api('POST', '/api/parties', null, { name: 'B', email: 'restart-b@example.com', partySize: 9 });
  check(b.status === 201 && b.cookie, 'B registered', b.status);

  const ci = await api('POST', '/api/me/check-in', a.cookie);
  check(ci.status === 200 && ci.json.result === 'checked_in', 'A checked in', ci);
  const endsAt = ci.json.dining.serviceEndsAt;

  const am = await api('GET', '/api/me', a.cookie);
  check(am.json.party.status === 'dining', 'A is dining', am.json);
  const bm = await api('GET', '/api/me', b.cookie);
  check(bm.json.party.status === 'waiting' && bm.json.waiting.aheadCount === 0 && bm.json.waiting.canCheckIn === false,
    'B waits at head (free seats 8 < 9)', bm.json);
  await withDb(async (db) => check((await seats(db)) === 2, 'seatsInUse = 2'));
  process.stdout.write(JSON.stringify({ a: a.cookie, b: b.cookie, endsAt }));
}

async function whileDown(state) {
  const reachable = await fetch(BASE + '/healthz').then(() => true).catch(() => false);
  check(!reachable, 'app process is down (connection fails)');
  const waitMs = Date.parse(state.endsAt) + 2000 - Date.now();
  console.error(`info: waiting ${Math.max(0, waitMs)}ms until A's serviceEndsAt (${state.endsAt}) has passed`);
  if (waitMs > 0) await sleep(waitMs);
  await withDb(async (db) => {
    check((await partyStatus(db, 'restart-a@example.com')) === 'dining', 'A still dining in DB after end time (no worker)');
    check((await seats(db)) === 2, 'seatsInUse still 2 while app is down');
    const a = await db.collection('parties').findOne({ emailNormalized: 'restart-a@example.com' });
    check(a.serviceEndsAt.toISOString() === new Date(state.endsAt).toISOString(), 'stored serviceEndsAt preserved');
  });
}

async function afterStart(state) {
  const readyMs = await waitFor(async () => (await fetch(BASE + '/readyz')).status === 200, 60_000, 'app ready');
  console.error(`info: app ready ${readyMs}ms after start`);
  const doneMs = await waitFor(async () => (await api('GET', '/api/me', state.a)).json.party.status === 'completed', 10_000, 'A completed');
  console.error(`info: A completed ${doneMs}ms after app became ready`);
  check(true, 'A completed by the startup sweep after restart (GET /api/me)');

  await withDb(async (db) => check((await seats(db)) === 0, 'seats returned exactly once (seatsInUse = 0)'));
  const bm = await api('GET', '/api/me', state.b);
  check(bm.json.waiting && bm.json.waiting.canCheckIn === true, 'B can check in now', bm.json);
  const ci = await api('POST', '/api/me/check-in', state.b);
  check(ci.status === 200 && ci.json.result === 'checked_in', 'B checked in', ci);

  // 재시작 후 여러 순회(1초 간격)가 지나도 A의 좌석을 다시 반환하지 않는다.
  await sleep(3500);
  await withDb(async (db) => {
    check((await seats(db)) === 9, 'seatsInUse = 9 after several sweeps (no double return)');
    check((await partyStatus(db, 'restart-a@example.com')) === 'completed', 'A stays completed');
    check((await partyStatus(db, 'restart-b@example.com')) === 'dining', 'B dining');
    check((await db.collection('parties').countDocuments({ status: 'completed' })) === 1, 'exactly one completed party');
  });
}

(async () => {
  const phase = process.argv[2];
  const state = process.env.STATE ? JSON.parse(process.env.STATE) : null;
  if (phase === 'setup') await setup();
  else if (phase === 'while-down') await whileDown(state);
  else if (phase === 'after-start') await afterStart(state);
  else throw new Error('unknown phase ' + phase);
})().catch((e) => { console.error('FAIL:', e.message); process.exit(1); });
