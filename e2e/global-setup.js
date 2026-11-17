// 전용 앱 인스턴스가 DB까지 준비될 때까지 기다린다.
module.exports = async () => {
  const base = process.env.BASE_URL || 'http://app-e2e:8080';
  const deadline = Date.now() + 60_000;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(base + '/readyz');
      if (res.status === 200) return;
      last = 'status ' + res.status;
    } catch (e) {
      last = String(e);
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error('app-e2e not ready: ' + last);
};
