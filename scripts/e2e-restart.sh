#!/usr/bin/env bash
# 앱 프로세스 재시작 복구 검증.
#
# 1. 임시 DB로 app-e2e를 띄우고, A를 체크인(식사 중)·B를 대기시킨다.
# 2. app-e2e 컨테이너를 SIGKILL로 강제 종료한다. (MongoDB와 저장된 종료 시각은 그대로)
# 3. A의 종료 예정 시각이 지날 때까지 기다리며, 그동안 아무것도 처리되지 않았음을 확인한다.
# 4. 같은 DB로 app-e2e를 다시 시작해 A 완료·좌석 반환·B 체크인·중복 반환 없음을 확인한다.
# 끝나면 app-e2e 컨테이너와 이번 임시 DB만 삭제한다. 개발용 app·waitlist DB는 건드리지 않는다.
set -euo pipefail
cd "$(dirname "$0")/.."

E2E_DATABASE="e2e_restart_$(date +%s)_${RANDOM}"
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

node_run() {
  docker compose --profile e2e run --rm --no-deps -e STATE="${STATE:-}" playwright node restart/restart-check.js "$@"
}

echo "== start app-e2e (db ${E2E_DATABASE})"
docker compose --profile e2e up -d --build --wait app-e2e
docker compose --profile e2e run --rm --no-deps playwright npm ci --no-audit --no-fund --loglevel=error

echo "== phase 1: setup"
STATE="$(node_run setup)"

echo "== kill app-e2e (SIGKILL)"
docker compose --profile e2e kill -s SIGKILL app-e2e
docker compose --profile e2e ps -a app-e2e --format '{{.Service}} {{.State}} {{.Status}}'

echo "== phase 2: while down"
node_run while-down

echo "== restart app-e2e with the same database"
docker compose --profile e2e start app-e2e

echo "== phase 3: after start"
node_run after-start
docker compose --profile e2e logs app-e2e --no-log-prefix 2>/dev/null | grep -o '"msg":"[^"]*"[^}]*' | grep -E 'worker started|service ended|database schema ready' || true

echo "== restart verification passed"
