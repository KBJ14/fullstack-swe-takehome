# Remote Waitlist Manager — 학습 프로젝트

TableCheck의 풀스택 SWE 과제인 **Remote Waitlist Manager**를 학습 목적으로 구현하기 위한 프로젝트입니다.

- 과제 원문 저장소: <https://github.com/TableCheck-Labs/fullstack-swe-takehome>
- 이 저장소의 [`README.md`](README.md)는 **과제 원문**이며, 수정하지 않고 그대로 둡니다.
  프로젝트 자체에 대한 설명과 설계 문서는 이 파일과 `docs/` 아래에 따로 작성합니다.

## 현재 상태

**최소 실행 환경 구성 단계**입니다.

- 구현된 것: Go HTTP 서버, MongoDB 단일 노드 replica set, Docker Compose 구성,
  상태 확인 경로(`/healthz`, `/readyz`)
- 아직 구현하지 않은 것: 대기 등록, 체크인, 식사 종료 작업, 쿠키, 화면
- `docs/`의 문서는 구현 전에 확정한 정책과 설계 초안을 담고 있습니다.

## 기술 구성 (계획)

| 영역 | 선택 |
| --- | --- |
| 백엔드 | Go |
| 화면 | Go HTML 템플릿(`html/template`), CSS, 의존성 없는 순수 JavaScript |
| 데이터베이스 | MongoDB (트랜잭션 사용을 위해 replica set 구성) |
| E2E 테스트 | Playwright |
| 로컬 실행 | Docker Compose |

### 렌더링 방식

- 서버가 HTML을 생성하는 **SSR 방식**입니다. **SPA로 만들지 않습니다.**
- 클라이언트 JavaScript는 내 상태 화면의 자동 갱신(폴링)과 남은 시간 표시처럼
  필요한 부분에만 사용합니다.

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

# 실제 MongoDB 트랜잭션 commit·rollback 통합 테스트 (임시 DB를 만들고 테스트 후 삭제)
docker compose --profile tools run --rm go go test -tags integration ./internal/database/...

# staticcheck
docker compose --profile tools run --rm go go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
```

### 종료

```sh
docker compose down      # 컨테이너와 네트워크만 삭제. 볼륨(DB 데이터)은 유지
```

다시 `docker compose up -d --wait`를 실행하면 기존 데이터와 replica set 설정으로 다시 준비됩니다.
`docker compose down -v`는 DB 데이터 볼륨까지 삭제하므로 주의하세요.

## 문서

- [docs/design.md](docs/design.md) — 원문 필수 요구사항, 자체 운영 정책, 데이터·API·동시성 설계 초안, 미정 사항
- [docs/verification.md](docs/verification.md) — 검증 시나리오와 검증 방법 (모두 미실행)
