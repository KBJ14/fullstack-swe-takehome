// 내 상태 카드 Island: 서버가 그린 #status-card를 최신 상태로 유지한다.
//
// - 초기 내용은 서버가 그린 HTML과 #initial-state(GET /api/me와 같은 형식)다.
// - GET /api/me를 1초 간격으로 조회한다. 응답이 끝난 뒤 다음 조회를 예약하므로 요청이 겹치지 않는다.
// - 체크인 중에는 조회를 멈추고, 진행 중이던 조회는 취소·무시한다. 체크인 결과를 반영한 뒤 다시 조회한다.
// - 화면 표시 규칙(phaseOf, aheadText, hintText, remainingText)은 서버(internal/web/view.go)와 같다.
// - DOM은 textContent와 hidden 속성만 바꾼다. API 응답을 HTML로 넣지 않는다.
// - 식별 쿠키는 브라우저가 처리한다. 이 스크립트는 토큰을 읽거나 저장하지 않는다.
(() => {
  'use strict';

  const card = document.getElementById('status-card');
  if (!card) return;

  const POLL_MS = 1000;
  const MAX_BACKOFF_MS = 5000;
  const RANK = { waiting: 0, dining: 1, completed: 2 };

  const el = {
    party: card.querySelector('[data-field="party"]'),
    ahead: card.querySelector('[data-field="ahead"]'),
    hint: card.querySelector('[data-field="hint"]'),
    remaining: card.querySelector('[data-field="remaining"]'),
    sections: card.querySelectorAll('[data-section]'),
    button: document.getElementById('check-in'),
    notice: document.getElementById('notice'),
    conn: document.getElementById('conn'),
    announce: document.getElementById('announce'),
  };

  // ---- 표시 규칙 (internal/web/view.go와 같아야 함) ----
  function phaseOf(v) {
    if (!v) return 'unregistered';
    switch (v.party.status) {
      case 'waiting': return v.waiting && v.waiting.canCheckIn ? 'ready' : 'waiting';
      case 'dining': return 'dining';
      default: return 'completed';
    }
  }
  function aheadText(n) { return n > 0 ? '내 앞에 ' + n + '팀' : '다음 순서입니다'; }
  function hintText(n) {
    return n > 0 ? '앞 팀이 입장하면 순서가 줄어듭니다.' : '빈 좌석이 생기면 체크인 버튼이 나타납니다.';
  }
  function remainingText(ms) {
    if (ms <= 0) return '이용 완료 확인 중';
    const s = Math.ceil(ms / 1000);
    const m = Math.floor(s / 60);
    return m > 0 ? m + '분 ' + String(s % 60).padStart(2, '0') + '초' : s + '초';
  }
  function partyLine(v) { return v ? v.party.name + ' · ' + v.party.partySize + '명' : ''; }

  const ANNOUNCE = {
    ready: '입장 차례입니다. 체크인해 주세요.',
    dining: '체크인되었습니다. 식사를 즐겨 주세요.',
    completed: '이용이 완료되었습니다.',
    unregistered: '등록 정보를 확인할 수 없습니다.',
  };

  // ---- 상태 ----
  let view = readInitial();
  let phase = card.dataset.phase;
  let clockOffset = view && view.dining ? Date.parse(view.dining.serverTime) - Date.now() : 0;
  let pollTimer = null;
  let countdownTimer = null;
  let inflight = null; // 진행 중인 조회의 AbortController
  let epoch = 0; // 체크인 시작·종료 때 증가. 이전 epoch의 조회 응답은 무시한다.
  let checkingIn = false;
  let verifyCheckIn = false; // 체크인 결과를 알 수 없어 다음 조회로 확인해야 함
  let connProblem = false;
  let failures = 0;
  let stopped = phase === 'completed' || phase === 'unregistered';
  let pageActive = true;

  function readInitial() {
    const node = document.getElementById('initial-state');
    try { return node ? JSON.parse(node.textContent) : null; } catch (e) { return null; }
  }

  // ---- 화면 반영 ----
  function setText(node, text) {
    if (node && node.textContent !== text) node.textContent = text;
  }

  function sameParty(a, b) {
    return a && b && a.party.registeredAt === b.party.registeredAt && a.party.name === b.party.name;
  }

  // 같은 팀의 상태는 대기 → 식사 → 완료로만 진행한다. 늦게 도착한 오래된 응답으로 되돌리지 않는다.
  function isRegression(next) {
    return sameParty(view, next) && RANK[next.party.status] < RANK[view.party.status];
  }

  function apply(next, receivedAt) {
    if (next && isRegression(next)) return;
    if (next && next.dining) clockOffset = Date.parse(next.dining.serverTime) - receivedAt;
    const prev = phase;
    view = next;
    phase = phaseOf(next);
    render();
    if (phase !== prev) {
      // 체크인 거부(409) 직후 대기 화면으로 돌아갈 때는 거부 사유를 계속 보여 준다.
      if (phase !== 'waiting') hideNotice();
      setText(el.announce, ANNOUNCE[phase] || '');
    }
    if (phase === 'completed' || phase === 'unregistered') stop();
  }

  function render() {
    card.dataset.phase = phase;
    el.sections.forEach((s) => { s.hidden = s.dataset.section !== phase; });
    setText(el.party, partyLine(view));
    el.party.hidden = !view;
    if (phase === 'waiting') {
      setText(el.ahead, aheadText(view.waiting.aheadCount));
      setText(el.hint, hintText(view.waiting.aheadCount));
    }
    renderButton();
    renderCountdown();
  }

  // 체크인 버튼 정책: 최신 조회가 성공한 상태에서만 누를 수 있다.
  // 조회가 실패하는 동안에는 canCheckIn이 오래된 값일 수 있으므로 버튼을 비활성화하고 이유를 보여 준다.
  // (서버는 체크인 요청마다 순서와 좌석을 다시 확인하므로, 이 정책은 안내의 정확성을 위한 것이다.)
  function renderButton() {
    if (!el.button) return;
    el.button.disabled = !(phase === 'ready' && !checkingIn && !connProblem && pageActive);
    setText(el.button, checkingIn ? '체크인 중…' : '체크인');
    if (checkingIn) el.button.setAttribute('aria-busy', 'true');
    else el.button.removeAttribute('aria-busy');
  }

  function renderCountdown() {
    if (phase !== 'dining' || !view || !view.dining) {
      if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
      return;
    }
    // 서버 시각 기준으로 계산한다. (로컬 시계 차이 보정, 탭 복귀 시에도 Date.now로 다시 계산)
    const serverNow = Date.now() + clockOffset;
    setText(el.remaining, remainingText(Date.parse(view.dining.serviceEndsAt) - serverNow));
    if (!countdownTimer) countdownTimer = setInterval(renderCountdown, 500);
  }

  function showNotice(text) { setText(el.notice, text); el.notice.hidden = false; }
  function hideNotice() { el.notice.hidden = true; setText(el.notice, ''); }

  function setConn(problem, kind) {
    connProblem = problem;
    if (problem) {
      let text = kind === 'server'
        ? '일시적인 오류로 최신 상태를 불러오지 못했습니다. 다시 시도하는 중입니다.'
        : '연결이 불안정합니다. 다시 연결하는 중입니다.';
      if (phase === 'ready') text += ' 연결되면 체크인할 수 있습니다.';
      setText(el.conn, text);
    }
    el.conn.hidden = !problem;
    card.classList.toggle('is-stale', problem);
    renderButton();
  }

  // ---- 조회 ----
  function canPoll() {
    return !stopped && pageActive && !checkingIn && document.visibilityState !== 'hidden';
  }

  function schedule(ms) {
    clearTimeout(pollTimer);
    pollTimer = null;
    if (canPoll()) pollTimer = setTimeout(poll, ms);
  }

  function abortInflight() {
    if (inflight) { inflight.abort(); inflight = null; }
  }

  async function readJSON(res) {
    try { return await res.json(); } catch (e) { return null; }
  }

  async function poll() {
    pollTimer = null;
    if (!canPoll() || inflight) return;
    const myEpoch = epoch;
    const ctrl = new AbortController();
    inflight = ctrl;
    let next = 'retry';
    try {
      const res = await fetch('/api/me', { signal: ctrl.signal, cache: 'no-store', headers: { Accept: 'application/json' } });
      const body = await readJSON(res);
      if (ctrl.signal.aborted || myEpoch !== epoch) { next = 'ignore'; return; }
      if (res.status === 200 && body && body.party) {
        setConn(false);
        apply(body, Date.now());
        settleVerification();
        next = 'ok';
      } else if (res.status === 401) {
        setConn(false);
        apply(null, Date.now());
        next = 'stop';
      } else {
        // 500 등: 완료나 미등록으로 오해하지 않고 마지막 상태를 유지한 채 다시 시도한다.
        setConn(true, 'server');
      }
    } catch (e) {
      if (ctrl.signal.aborted || myEpoch !== epoch) { next = 'ignore'; return; }
      setConn(true, 'network');
    } finally {
      if (inflight === ctrl) inflight = null;
      if (next === 'ok') { failures = 0; schedule(POLL_MS); }
      if (next === 'retry') { failures += 1; schedule(Math.min(POLL_MS * 2 ** failures, MAX_BACKOFF_MS)); }
    }
  }

  function stop() {
    stopped = true;
    clearTimeout(pollTimer);
    pollTimer = null;
    abortInflight();
  }

  // ---- 체크인 ----
  function settleVerification() {
    if (!verifyCheckIn) return;
    verifyCheckIn = false;
    if (phase === 'ready') showNotice('체크인이 처리되지 않았습니다. 다시 시도해 주세요.');
    else hideNotice();
  }

  async function checkIn() {
    if (checkingIn || phase !== 'ready' || connProblem) return; // 중복 클릭 방지
    checkingIn = true;
    epoch += 1; // 진행 중인 조회 응답을 무효로 한다
    abortInflight();
    clearTimeout(pollTimer);
    pollTimer = null;
    hideNotice();
    renderButton();

    let resume = true;
    try {
      // 요청 본문 없음 (API 계약). 쿠키는 브라우저가 붙인다.
      const res = await fetch('/api/me/check-in', { method: 'POST', cache: 'no-store', headers: { Accept: 'application/json' } });
      const body = await readJSON(res);
      if (res.status === 200 && body && body.party) {
        setConn(false);
        apply(body, Date.now());
      } else if (res.status === 409) {
        showNotice((body && body.error && body.error.message) || '지금은 체크인할 수 없습니다.');
      } else if (res.status === 401) {
        apply(null, Date.now());
        resume = false;
      } else if (res.status === 403) {
        showNotice('요청이 거부되었습니다. 페이지를 새로고침한 뒤 다시 시도해 주세요.');
      } else {
        // 처리 여부를 알 수 없다. 실패로 단정하지 않고 상태 조회로 확인한다.
        showNotice('체크인 결과를 확인하는 중입니다.');
        verifyCheckIn = true;
      }
    } catch (e) {
      showNotice('연결 문제로 체크인 결과를 확인하는 중입니다.');
      verifyCheckIn = true;
    } finally {
      checkingIn = false;
      epoch += 1;
      renderButton();
      if (resume && !stopped) poll(); // 최신 상태로 다시 확인하고 조회를 재개한다
    }
  }

  if (el.button) el.button.addEventListener('click', checkIn);

  // ---- 화면 숨김·복귀, 페이지 이동 ----
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      clearTimeout(pollTimer);
      pollTimer = null;
      abortInflight();
    } else {
      renderCountdown();
      poll(); // 다시 보이면 즉시 최신 상태 확인
    }
  });

  window.addEventListener('pagehide', () => {
    pageActive = false;
    clearTimeout(pollTimer);
    pollTimer = null;
    abortInflight();
    if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
  });

  window.addEventListener('pageshow', (event) => {
    if (!event.persisted) return;
    // 뒤로가기·앞으로가기 캐시에서 복원됨: 한 번은 최신 상태를 확인한다.
    pageActive = true;
    stopped = false;
    render();
    poll();
  });

  // ---- 시작 ----
  render();
  schedule(POLL_MS);
})();
