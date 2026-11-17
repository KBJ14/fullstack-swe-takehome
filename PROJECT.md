# Remote Waitlist Manager — 학습 프로젝트

TableCheck의 풀스택 SWE 과제인 **Remote Waitlist Manager**를 학습 목적으로 구현하기 위한 프로젝트입니다.

- 과제 원문 저장소: <https://github.com/TableCheck-Labs/fullstack-swe-takehome>
- 이 저장소의 [`README.md`](README.md)는 **과제 원문**이며, 수정하지 않고 그대로 둡니다.
  프로젝트 자체에 대한 설명과 설계 문서는 이 파일과 `docs/` 아래에 따로 작성합니다.

## 현재 상태

손님이 브라우저로 등록부터 체크인·식사 완료·재등록까지 쓸 수 있는 상태입니다.

- 구현된 것: Go HTTP 서버, MongoDB 단일 노드 replica set, Docker Compose 구성,
  상태 확인 경로(`/healthz`, `/readyz`), **대기 등록 API(`POST /api/parties`)와 식별 쿠키·CSRF 대응**,
  **상태 조회 API(`GET /api/me`)**, **체크인 API(`POST /api/me/check-in`)**,
  **서버 내부 식사 종료·좌석 반환 작업**(1초 간격, 브라우저 요청과 무관),
  **손님용 화면**(Go SSR + 순수 JavaScript Islands, Playwright E2E)
- `docs/`의 문서는 구현 전에 확정한 정책과 설계 초안을 담고 있습니다.

## 기술 구성

| 영역 | 선택 |
| --- | --- |
| 백엔드 | Go |
| 화면 | Go HTML 템플릿(`html/template`), CSS, 의존성 없는 순수 JavaScript |
| 데이터베이스 | MongoDB (트랜잭션 사용을 위해 replica set 구성) |
| E2E 테스트 | Playwright |
| 로컬 실행 | Docker Compose |

### 렌더링 방식

- 서버가 실제 내용이 있는 HTML을 생성하는 **SSR 방식**이고, **SPA가 아닙니다.** (과제의 패턴 중 **Islands**)
- JavaScript는 두 영역(Island)만 담당합니다: 등록 폼(`join.js`), 내 상태 카드(`status.js`). 페이지 이동은 일반 링크입니다.
- 프런트엔드 라이브러리·빌드 도구는 없습니다. 템플릿·CSS·JS는 Go 바이너리에 포함(`embed`)되어 앱 서버가 함께 제공합니다.
- 자세한 경계와 규칙: [docs/design.md](docs/design.md) 2.10

### 화면 경로

| 경로 | 화면 |
| --- | --- |
| `http://127.0.0.1:8080/` | 등록이 있으면 내 상태 화면, 없으면 등록 화면으로 이동 |
| `/join` | 대기 등록 (진행 중인 등록이 있으면 내 상태 화면으로 이동) |
| `/status` | 내 상태: 대기 중(내 앞 N팀) / 체크인 가능(체크인 버튼) / 식사 중(남은 시간) / 이용 완료(다시 등록) |

자동 갱신·등록·체크인에는 JavaScript가 필요합니다. JavaScript가 꺼져 있으면 내용만 보이고 안내 문구가 표시됩니다.

## 로컬 실행

아래 명령은 Docker 29.9.2 / Docker Compose v5.6.1 환경(WSL2 + Docker Desktop)에서 실제로 실행해 확인했습니다.
Go와 MongoDB는 컨테이너 안에서 실행하므로 호스트에 Go를 설치할 필요가 없습니다.

### 구성

| 서비스 | 이미지 | 역할 |
| --- | --- | --- |
| `mongo` | `mongo:8.0.38` | MongoDB. `--replSet rs0`로 실행. **호스트에 포트를 공개하지 않음** |
| `mongo-init` | `mongo:8.0.38` | replica set 초기화 후 종료하는 일회성 작업 ([deploy/mongo/init-replica-set.js](deploy/mongo/init-replica-set.js)) |
| `app` | 직접 빌드 (`golang:1.27.4-alpine3.24` → `distroless/static-debian13:nonroot`) | Go 서버. `127.0.0.1:${APP_PORT:-8080}`에 공개 |
| `go` | `golang:1.27.4-alpine3.24` | 테스트·정적 검사용 도구 (`tools` profile에서만 사용) |

- **단일 노드 replica set은 로컬 학습용입니다.** 트랜잭션을 쓰기 위한 구성이며 고가용성 구성이 아닙니다.
- replica set 멤버는 Compose 서비스 이름 `mongo:27017`로 광고하므로, 같은 Compose 네트워크의 앱만 접속할 수 있습니다.
- 초기화 스크립트는 이미 초기화돼 있으면 아무것도 바꾸지 않으므로 재실행해도 안전합니다.
  저장된 설정의 replica set 이름이나 멤버 호스트가 기대값과 다르면 바꾸지 않고 실패합니다.
- 앱은 `mongo-init`이 성공적으로 끝나고 `mongo`가 healthy(쓰기 가능한 primary)가 된 뒤에 시작합니다.
- DB 데이터는 named volume `waitlist-takehome_mongo-data`, `waitlist-takehome_mongo-config`에 보존됩니다.

### 설정

기본값으로 실행할 수 있습니다. 값을 바꾸려면 [.env.example](.env.example)을 `.env`로 복사해 수정합니다.
`.env`는 Git에서 제외됩니다.

- `COOKIE_SECURE`: 식별 쿠키의 `Secure` 속성. 로컬 HTTP에서는 기본값 `false`, **HTTPS로 배포할 때는 `true`**
- `SERVICE_END_INTERVAL`: 식사 종료·좌석 반환 작업의 확인 간격. 기본값 `1s`

### 실행

```sh
docker compose up -d --build --wait
```

### 상태 확인

```sh
curl -i http://127.0.0.1:8080/healthz   # 200 {"status":"ok"}        서버 생존
curl -i http://127.0.0.1:8080/readyz    # 200 {"status":"ready"}     DB 연결 + replica set primary
                                        # 503 {"status":"not_ready"} 제한 시간(기본 2초) 안에 확인 실패
```

`/readyz`의 실패 원인은 응답에 포함하지 않고 `docker compose logs app`에만 기록합니다.

### 테스트와 정적 검사

```sh
# 포맷, vet, 단위 테스트
docker compose --profile tools run --rm go sh -c 'gofmt -l . && go vet ./... && go test ./...'

# 실제 MongoDB 통합 테스트: 트랜잭션 commit·rollback, 등록 API (테스트마다 임시 DB를 만들고 테스트 후 삭제)
docker compose --profile tools run --rm go go test -tags integration ./...

# staticcheck
docker compose --profile tools run --rm go go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
```

검증 결과는 [docs/verification.md](docs/verification.md)의 각 검증 기록에 있습니다.

### E2E 테스트 (Playwright)

```sh
scripts/e2e.sh                       # 전체
scripts/e2e.sh tests/flow.spec.js    # 일부 파일만
```

- 개발용 `app`·`waitlist` DB와 분리된 `app-e2e` 인스턴스를 임시 DB(`e2e_<시각>_<난수>`)로 띄우고,
  공식 Playwright 이미지(`mcr.microsoft.com/playwright:v1.65.1-noble`, `@playwright/test 1.65.1` 고정)에서 실행합니다.
  두 컨테이너 모두 호스트 포트를 공개하지 않으므로 실행 중인 개발 서비스와 충돌하지 않습니다.
- 끝나면 `app-e2e` 컨테이너와 이번에 만든 임시 DB만 삭제합니다. (`mongo` 서비스는 실행 중이어야 함)
- 테스트 도구(Node 패키지)는 `e2e/`에만 있고, 앱 화면은 의존성 없는 순수 JavaScript입니다.
- 실패 시 스크린샷·trace는 `e2e/test-results/`, HTML 보고서는 `e2e/playwright-report/`에 남습니다. (Git 제외)

### 대기 등록 API 예시

```sh
curl -i -X POST http://127.0.0.1:8080/api/parties \
  -H 'Content-Type: application/json' \
  -d '{"name":"Kim","email":"kim@example.com","partySize":2}'
```

- 새 등록이면 201과 `waitlist_token` 쿠키를 받습니다. 같은 쿠키로 다시 보내면 200(기존 등록)입니다.
- 이 요청은 영구 DB(`waitlist`)에 실제 팀을 만듭니다. 그 팀이 체크인해 식사를 마쳐 완료되기 전까지는 같은 이메일로 다시 등록하면 409입니다.
- 상세 규칙(입력 검증, 응답 코드, 쿠키, CSRF)은 [docs/design.md](docs/design.md) 2.7~2.9를 참고하세요.

### 상태 조회 API 예시

```sh
# 등록 응답의 쿠키를 파일에 저장했다가 조회에 사용
curl -s -c cookies.txt -X POST http://127.0.0.1:8080/api/parties \
  -H 'Content-Type: application/json' \
  -d '{"name":"Kim","email":"kim@example.com","partySize":2}'
curl -s -b cookies.txt http://127.0.0.1:8080/api/me

# 체크인 (본문 없음). 맨 앞이고 좌석이 충분할 때만 성공, 아니면 409
curl -s -b cookies.txt -X POST http://127.0.0.1:8080/api/me/check-in
```

- 체크인한 팀은 인원 × 3초 뒤 서버의 식사 종료 작업이 완료 처리하고 좌석을 반환합니다(확인 간격 1초만큼 늦을 수 있음).
  이후 `GET /api/me`는 완료 상태를 보여 주고, 같은 이메일로 다시 등록할 수 있습니다.
- 이 예시는 영구 DB(`waitlist`)에 실제 팀을 만듭니다. 완료된 팀 문서는 삭제되지 않고 남습니다.

- 쿠키가 없거나 연결된 팀이 없으면 401입니다. 응답 형식은 [docs/design.md](docs/design.md) 2.7 `GET /api/me`를 참고하세요.
- `cookies.txt`에는 식별 토큰 원문이 들어 있으므로 공유하거나 커밋하지 마세요.

### 종료

```sh
docker compose down      # 컨테이너와 네트워크만 삭제. 볼륨(DB 데이터)은 유지
```

다시 `docker compose up -d --wait`를 실행하면 기존 데이터와 replica set 설정으로 다시 준비됩니다.
`docker compose down -v`는 DB 데이터 볼륨까지 삭제하므로 주의하세요.

## 문서

- [docs/design.md](docs/design.md) — 원문 필수 요구사항, 자체 운영 정책, 데이터·API·동시성 설계 초안, 미정 사항
- [docs/verification.md](docs/verification.md) — 검증 시나리오와 검증 방법 (모두 미실행)
