#!/usr/bin/env bash
# Playwright E2E 실행.
#
# - 개발용 app·waitlist DB와 분리된 앱 인스턴스(app-e2e)를 임시 DB(e2e_<시각>_<난수>)로 띄운다.
# - 공식 Playwright 컨테이너에서 테스트를 실행한다. 호스트에 포트를 공개하지 않는다.
# - 끝나면(성공·실패 모두) app-e2e 컨테이너를 지우고 이번에 만든 임시 DB만 삭제한다.
#
# 사용: scripts/e2e.sh [playwright test 인자...]   예) scripts/e2e.sh tests/flow.spec.js
set -euo pipefail
cd "$(dirname "$0")/.."

E2E_DATABASE="e2e_$(date +%s)_${RANDOM}"
E2E_UID="$(id -u)"
E2E_GID="$(id -g)"
export E2E_DATABASE E2E_UID E2E_GID

cleanup() {
  docker compose --profile e2e rm -sf app-e2e >/dev/null 2>&1 || true
  docker compose exec -T mongo mongosh --quiet --eval "
    const name = '${E2E_DATABASE}';
    if (!/^e2e_/.test(name)) { throw new Error('refusing to drop ' + name); }
    db.getSiblingDB(name).dropDatabase();
  " >/dev/null || echo "warning: could not drop ${E2E_DATABASE}" >&2
}
trap cleanup EXIT

docker compose --profile e2e up -d --build --wait app-e2e
docker compose --profile e2e run --rm --no-deps playwright \
  sh -c 'npm ci --no-audit --no-fund --loglevel=error && npx playwright test "$@"' -- "$@"
