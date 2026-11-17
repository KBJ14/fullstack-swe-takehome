# 검증 시나리오

[design.md](design.md)의 정책과 설계를 확인하기 위한 시나리오입니다.

> 실행하지 않은 테스트를 통과한 것으로 기록하지 않습니다.
> 모든 시나리오(1~17)를 단위·통합 테스트 또는 Playwright E2E로 실행했습니다. 각 시나리오의 "실행 상태"에 실제 수행 범위를 적었습니다.
> 화면이 필요한 부분은 "손님 화면 E2E 검증 기록"(맨 아래)에 있습니다. 이전 기록에서 "E2E 미실행"으로 적은 화면 부분은 그 기록으로 갱신됩니다.
> 실행 결과는 맨 아래 "등록 API 검증 기록", "상태 조회 API 검증 기록", "체크인 API 검증 기록", "식사 종료 작업 검증 기록", "손님 화면 E2E 검증 기록"에 있습니다.

## 검증 방법 구분

| 구분 | 대상 |
| --- | --- |
| **E2E** (Playwright) | 화면과 사용자 흐름 |
| **통합 테스트** (서버 + 실제 MongoDB replica set) | 동시 요청, 중복 제한, 트랜잭션, 종료 작업 |
| **단위 테스트** | 필요한 순수 계산 규칙 (이메일 정규화, 인원수 검증, 식사 시간·남은 시간 계산, 체크인 가능 판단 등) |

시나리오의 "정원 10석", "인원당 3초"는 design.md의 정책을 따릅니다.

---

## 1. 정상 등록

- **준비 상태**: 대기열과 사용 중 좌석이 비어 있음. 브라우저에 토큰 쿠키 없음
- **행동**: 등록 화면에서 이름, 올바른 이메일, 인원수 2를 입력하고 등록
- **기대 결과**: 등록이 성공하고 내 상태 화면으로 이동. 토큰 쿠키가 발급되고, DB에는 대기 중 상태의 팀이 저장되며 토큰 원문이 아닌 검증값이 저장됨. 좌석이 충분하고 맨 앞이므로 체크인 가능으로 표시
- **검증 방법**: E2E(화면 흐름), 통합 테스트(저장 내용, 토큰 원문 미저장)
- **실행 상태**: 통합 테스트 통과(API 응답·쿠키·저장 내용·토큰 원문 미저장). 화면이 없으므로 "내 상태 화면 이동"과 "체크인 가능 표시"는 미실행. E2E 미실행

## 2. 인원수 경계값 1명·10명 허용

- **준비 상태**: 대기열 비어 있음
- **행동**: 서로 다른 이메일로 인원수 1, 10을 각각 등록
- **기대 결과**: 두 요청 모두 등록 성공
- **검증 방법**: 단위 테스트(인원수 검증 규칙), 통합 테스트(API), E2E(폼 입력 1건 이상)
- **실행 상태**: 단위·통합 테스트 통과. E2E 미실행

## 3. 인원수 0명·음수·소수·11명 이상·빈 값 거부

- **준비 상태**: 대기열 비어 있음
- **행동**: 인원수 `0`, `-1`, `2.5`, `11`, 빈 값으로 각각 등록 요청. 브라우저 폼 검증을 거치지 않는 직접 API 요청도 포함
- **기대 결과**: 모든 요청이 거부되고 팀이 저장되지 않음. 화면에서는 인원수 입력 오류가 안내됨
- **검증 방법**: 단위 테스트(검증 규칙), 통합 테스트(직접 API 요청), E2E(오류 안내 표시)
- **실행 상태**: 단위·통합 테스트 통과(문자열 `"2"`, `2.0`, `1e1`, 불리언, `null` 등 잘못된 타입 포함). E2E 미실행

## 4. 필수 입력과 이메일 형식 검증

- **준비 상태**: 대기열 비어 있음
- **행동**: 이름 누락, 이메일 누락, 형식이 잘못된 이메일(예: `abc`, `a@`)로 각각 등록 요청
- **기대 결과**: 모든 요청이 거부되고 팀이 저장되지 않음. 화면에서 해당 필드의 오류가 안내되고 입력한 다른 값은 유지되는지 확인
- **검증 방법**: 단위 테스트(검증 규칙), 통합 테스트(직접 API 요청), E2E(오류 안내)
- **실행 상태**: 단위·통합 테스트 통과. "입력한 다른 값 유지"는 화면 기능이라 미실행. E2E 미실행
- **비고**: 이메일 형식 기준은 design.md 2.7

## 5. 같은 이메일의 중복 등록 및 동시 등록 방지

- **준비 상태**: (a) `a@example.com`으로 대기 중인 팀 존재 / (b) `a@example.com`으로 식사 중인 팀 존재 / (c) 해당 이메일의 진행 중인 팀 없음
- **행동**: (a)(b)에서 같은 이메일로 등록 요청. (c)에서 같은 이메일로 여러 등록 요청을 동시에 전송
- **기대 결과**: (a)(b)는 거부. (c)는 정확히 한 요청만 성공하고 나머지는 거부되며, DB에 해당 이메일의 진행 중인 팀은 하나뿐
- **검증 방법**: 통합 테스트(동시 요청, DB 수준 중복 방지 확인), E2E(중복 오류 안내)
- **실행 상태**: (a)(b)(c) 통합 테스트 통과. (b)는 식사 중 상태를 DB fixture로 준비. 동시 요청 테스트는 10회 반복 실행해 모두 통과. E2E 미실행

## 6. 이메일 공백·대소문자 정규화

- **준비 상태**: `a@example.com`으로 대기 중인 팀 존재
- **행동**: `  A@Example.COM  `으로 등록 요청
- **기대 결과**: 같은 이메일로 판단해 거부
- **검증 방법**: 단위 테스트(정규화 함수), 통합 테스트(API와 DB 중복 방지가 정규화 값 기준인지)
- **실행 상태**: 단위·통합 테스트 통과

## 7. 빈 좌석 4석에서 앞 팀 5명·뒤 팀 2명이면 두 팀 모두 대기

- **준비 상태**: 식사 중인 팀들로 사용 중 좌석 6석(빈 좌석 4석). 대기열에 5명 팀(앞), 2명 팀(뒤) 순서로 등록
- **행동**: 두 팀의 상태 조회, 뒤 팀의 체크인 요청
- **기대 결과**: 두 팀 모두 대기 중이며 체크인 가능이 아님. 뒤 팀의 체크인 요청은 거부되고 좌석 변화 없음. 앞 팀 수는 앞 팀 0, 뒤 팀 1
- **검증 방법**: 단위 테스트(체크인 가능 판단), 통합 테스트(체크인 거부와 좌석 불변), E2E(두 브라우저 컨텍스트에서 체크인 버튼 미표시)
- **실행 상태**: 단위·통합 테스트 통과 — 상태 조회(`TestMeCanCheckInRules`: 두 팀 모두 체크인 불가, 앞 팀 수 0·1)와 체크인(`TestCheckInBehindHeadIsRejectedEvenWithSeats`, `TestCheckInHeadWithoutEnoughSeatsLeavesNoPartialChanges`: 뒤 팀 `not_your_turn`, 앞 팀 `not_enough_seats`, 데이터 변경 없음). 좌석은 fixture로 준비. E2E 미실행

## 8. 체크인 시 대기열 제외와 정확한 좌석 차감

- **준비 상태**: 사용 중 좌석 0. 대기열에 3명 팀 A(앞), 2명 팀 B(뒤)
- **행동**: A가 체크인
- **기대 결과**: A는 식사 중, 사용 중 좌석 3, A의 종료 예정 시각 = 체크인 시각 + 9초. A는 대기열과 B의 앞 팀 수에서 제외되어 B의 앞 팀 수 0, B는 체크인 가능
- **검증 방법**: 통합 테스트(상태·좌석·종료 시각), E2E(A 화면의 남은 시간 표시, B 화면 변화)
- **실행 상태**: 통합 테스트 통과(`TestCheckInSuccessWithIssuedCookie`: 4명 팀으로 실행 — 좌석 4, 종료 = 체크인 + 12초, 응답 시각 = 저장 시각, 이후 `GET /api/me`에서 B의 앞 팀 0·체크인 가능). E2E 미실행

## 9. 체크인 반복 요청의 중복 차감 방지

- **준비 상태**: 체크인 가능한 2명 팀, 사용 중 좌석 0
- **행동**: (a) 체크인 요청을 여러 번 순차 전송 / (b) 같은 팀의 체크인 요청을 동시에 여러 번 전송
- **기대 결과**: 사용 중 좌석은 2로 한 번만 증가. 첫 성공 이후 요청은 좌석을 바꾸지 않고 현재 상태(식사 중)를 반환. 종료 예정 시각도 바뀌지 않음
- **검증 방법**: 통합 테스트
- **실행 상태**: 통합 테스트 통과 — (a) `TestCheckInRepeatedSequentiallyIsIdempotent`(3명), (b) `TestCheckInConcurrentSameParty`(4명, 동시 15건: `checked_in` 1건, 나머지 `already_dining`, 모든 응답의 종료 시각 동일). `-count=10` 반복 통과

## 10. 동시 체크인의 선착순·10석 제한

- **준비 상태**: 여러 팀이 등록 순서대로 대기 중이며 인원 합계가 10을 넘음 (예: 6명, 5명, 1명)
- **행동**: 모든 팀의 체크인 요청을 동시에 전송. 이후 앞 팀이 입장한 상태에서도 남은 팀들의 체크인 요청을 동시에 전송
- **기대 결과**: 각 시점에 맨 앞이면서 좌석이 충분한 팀만 체크인됨. 순서를 건너뛴 체크인 없음(예: 6명 팀 입장 후 5명 팀이 좌석 부족으로 대기하면 1명 팀도 대기). 사용 중 좌석은 어느 시점에도 0~10 범위를 벗어나지 않음
- **검증 방법**: 통합 테스트(동시 요청 반복 실행, 트랜잭션 충돌 재시도 경로 포함)
- **실행 상태**: 통합 테스트 통과(`TestCheckInConcurrentDifferentPartiesKeepsOrderAndCapacity`: 6·5·1을 포함한 4가지 구성). 같은 라운드에서 뒤 팀의 409는 허용하고, 대기 팀이 진행이 멈출 때까지 다시 요청. 매 라운드 후 추월 없음·좌석 = 식사 중 인원 합 ≤ 10·200/409 외 응답 없음을 확인. `-count=10` 반복 통과

## 11. 같은 브라우저 종료 후 재접속

- **준비 상태**: 한 브라우저에서 등록해 토큰 쿠키가 있는 상태
- **행동**: 새로고침, 브라우저 종료 후 같은 저장 상태(쿠키 유지)로 다시 접속
- **기대 결과**: 등록 화면이 아니라 내 팀의 최신 상태가 표시됨. 쿠키가 없는 새 브라우저 컨텍스트에서는 복원되지 않음(제외 범위)
- **검증 방법**: E2E(Playwright 저장 상태를 이용해 브라우저 컨텍스트 재생성)
- **실행 상태**: E2E 통과 — `lifecycle.spec.js`: 디스크 프로필(`launchPersistentContext`)로 브라우저 프로세스를 **완전히 종료한 뒤 같은 프로필로 다시 실행**해 `/`에서 상태 화면이 복원됨. 다른 프로필(쿠키 없음)은 미등록 화면. 새로고침·재방문은 다른 E2E에서도 반복 확인

## 12. 자동 갱신으로 앞 팀 수와 체크인 가능 여부 반영

- **준비 상태**: 두 브라우저 컨텍스트에서 팀 A(앞), 팀 B(뒤) 등록. B 화면에서 앞 팀 수 1
- **행동**: B 화면을 새로고침하지 않은 채 A가 체크인
- **기대 결과**: B 화면이 폴링으로 갱신되어 앞 팀 수가 0이 되고, 좌석이 충분하면 체크인 버튼이 표시됨
- **검증 방법**: E2E
- **실행 상태**: E2E 통과 — `queue.spec.js`: 두 브라우저 컨텍스트, B는 "내 앞에 1팀"(서버 렌더링 직후와 갱신 후 동일) → A 체크인 후 새로고침 없이 B에 체크인 버튼. 좌석 부족으로 맨 앞 팀이 기다리면 작은 뒤 팀도 대기하고, 좌석 반환 후 맨 앞 팀에 버튼이 자동으로 나타남

## 13. 브라우저가 닫혀 있어도 식사 종료와 좌석 반환

- **준비 상태**: 2명 팀이 체크인해 식사 중, 사용 중 좌석 2
- **행동**: 해당 팀의 브라우저를 닫고 상태 조회 요청 없이 종료 예정 시각(6초) + 확인 주기 이상 대기
- **기대 결과**: 팀은 완료 상태가 되고 사용 중 좌석 0으로 반환됨. 다음 대기 팀이 체크인 가능해짐
- **검증 방법**: 통합 테스트(상태 조회 없이 DB 확인)
- **실행 상태**: 통합 테스트 통과 — `TestWorkerCompletesWithoutRequests`(실제 작업 주기 50ms, HTTP 요청 없이 4명 팀이 종료 시각 후 완료·좌석 0. 종료 시각은 fixture로 300ms 뒤로 설정해 6초 대기를 줄임), `TestAfterServiceEndStatusSeatsAndReRegistration`(종료 후 다음 팀 `canCheckIn` 참, 자동 체크인 없음)

## 14. 반복 종료 작업의 중복 반환 방지

- **준비 상태**: 종료 예정 시각이 지난 식사 중 팀(3명)과 다른 식사 중 팀(4명), 사용 중 좌석 7
- **행동**: 종료 처리 작업을 여러 번 순차 실행하고, 동시에도 여러 번 실행
- **기대 결과**: 종료 시각이 지난 팀만 한 번 완료 처리되어 사용 중 좌석 4. 반복 실행해도 좌석이 더 줄지 않고 0 미만이 되지 않음
- **검증 방법**: 통합 테스트
- **실행 상태**: 통합 테스트 통과 — `TestCompleteDueOnlyTouchesDueDiningParties`(3명·4명 구성 그대로: 좌석 7 → 4), `TestCompleteDueRepeatedAndConcurrentReturnsSeatsOnce`(동시 12회 + 순차 3회: 완료 합계 2건, 좌석 정확), `TestCompleteDueUnderflowIsRolledBackAndDoesNotBlockOthers`(음수가 될 상황은 취소·보정 없음). `-count=10` 반복 통과

## 15. 이용 완료 후 같은 이메일로 재등록

- **준비 상태**: `a@example.com` 팀이 완료 상태이며 같은 브라우저에 해당 토큰 쿠키가 있음
- **행동**: 내 상태 화면의 재등록 경로로 같은 이메일을 사용해 등록
- **기대 결과**: 새 팀으로 등록되어 대기열 맨 뒤에 들어가고, 새 토큰이 발급되어 내 상태 화면이 새 팀을 표시. 이전 완료 기록은 진행 중 팀으로 취급되지 않음
- **검증 방법**: E2E(화면 흐름), 통합 테스트(중복 판단에서 완료 팀 제외)
- **실행 상태**: 통합 테스트 통과(완료 상태는 DB fixture로 준비, 쿠키 없이 / 완료 쿠키로 두 경우). 화면의 재등록 경로는 미구현이라 E2E 미실행

## 16. 이용 완료 팀이 체크인을 재요청해도 식사 재시작 안 됨

- **준비 상태**: 완료 상태인 팀, 해당 팀의 토큰
- **행동**: 체크인 API 요청 (화면을 거치지 않는 직접 요청 포함)
- **기대 결과**: 상태는 완료로 유지되고 좌석·종료 시각 변화 없음. 응답은 현재 상태(완료)를 나타냄
- **검증 방법**: 통합 테스트
- **실행 상태**: 통합 테스트 통과(`TestCheckInCompletedIsRejected`: 409 `already_completed`, 데이터 변경 없음. 완료 상태는 fixture). 응답은 오류 본문이며 완료 상태는 `GET /api/me`로 확인

## 17. 서버 재시작 후 종료 처리 복구

- **준비 상태**: 식사 중인 팀이 있는 상태에서 서버 프로세스 중지
- **행동**: 종료 예정 시각이 지날 때까지 기다린 뒤 서버를 다시 시작
- **기대 결과**: 재시작 후 첫 확인 주기 안에 DB에 저장된 종료 시각을 기준으로 완료 처리되고 좌석이 한 번만 반환됨
- **검증 방법**: 통합 테스트(서버 또는 종료 작업을 중지·재시작하고 실제 MongoDB의 데이터 유지 확인)
- **실행 상태**: 통합 테스트 통과 — `TestWorkerCompletesOverdueOnStartup`(종료 시각이 1분 지난 팀이 있는 상태에서 작업을 새로 시작하면, 간격을 1시간으로 둬도 시작 직후 순회에서 완료. 아직 종료 전인 팀은 유지), `TestWorkerStopsOnCancel`(중지 후에는 처리하지 않음). 실제 서버 프로세스를 멈췄다 켜는 시험은 영구 DB에 데이터를 만들어야 해서 하지 않았고, 컨테이너 재시작으로는 작업 시작·정지 로그 순서만 확인

---

## 등록 API 검증 기록

- **실행일**: 2026-11-17
- **환경**: Docker Compose `go` 도구 서비스(`golang:1.27.4-alpine3.24`), `mongo:8.0.38` 단일 노드 replica set
- **데이터**: 통합 테스트마다 고유한 임시 DB(`it_api_*`, `it_store_*`)를 만들고 테스트 후 그 DB만 삭제. 실행 후 남은 임시 DB 없음을 확인
- **fixture**: 식사 중·완료 상태는 테스트에서 DB의 `status`를 직접 바꿔 준비 (체크인·종료 기능은 구현하지 않음)

### 단위 테스트 (`go test ./...`) — 통과

| 영역 | 확인 내용 |
| --- | --- |
| `internal/party` | 이메일 정규화, 이름 공백 제거·필수·100자 제한, 이메일 형식·254자 제한, 인원수 1·10 허용 / 0·음수·11 이상 거부 |
| `internal/token` | 발급 형식·고유성, 잘못된 형식 거부, 해시의 결정성과 원문 비노출 |
| `internal/api` | JSON 문법 오류·배열·알 수 없는 필드·추가 JSON 값·4KiB 초과 → 400; 필드별 필수·타입·범위 오류(`"2"`, `2.5`, `2.0`, `1e1`, `true`, `null`, `""`, 매우 큰 수) → 400; JSON이 아닌 Content-Type → 415; 201 쿠키 속성(HttpOnly, SameSite=Lax, Path=/, Max-Age 7일, Secure 설정 반영); 응답 본문에 토큰·해시·이메일·순번 없음; 409 본문에 팀 정보·쿠키 없음; 500 상세 숨김; GET 405 |
| CSRF (`internal/api`) | 허용: `Sec-Fetch-Site: same-origin`, `none`, 같은 `Origin`, 헤더 없는 비브라우저 요청. 거부(403, 저장소 호출·쿠키 없음): `cross-site`, `same-site`, 다른 `Origin` |
| `internal/health` | 기존 `/healthz`, `/readyz` 동작 유지 |

### 통합 테스트 (`go test -tags integration ./...`) — 통과

| 테스트 | 시나리오 |
| --- | --- |
| `TestRegisterCreatesPartyAndCookieIdentifiesIt` | 정상 등록 201, 저장 값 정규화, 순번 1, 발급 쿠키의 해시로 팀 조회 가능, DB에 토큰 원문 없음, 좌석 0 유지 |
| `TestPartySizeBoundariesStored` | 1명·10명 저장 |
| `TestInvalidInputCreatesNothing` | 잘못된 입력은 팀도 순번도 만들지 않음 |
| `TestSameEmailWithoutCookieIs409AndHidesParty` | 쿠키 없이 공백·대소문자만 다른 같은 이메일 → 409, 안내 문구 일치, 기존 팀 정보 없음 |
| `TestConcurrentSameEmailCreatesOneActiveParty` | 같은 이메일(대소문자·공백 변형) 동시 30건 → 201 한 건, 409 스물아홉 건(500 없음), 순번 하나만 소모 |
| `TestConcurrentDifferentEmailsGetUniqueSequences` | 다른 이메일 동시 20건 → 모두 201, 순번 1~20 중복 없음, 좌석 0 유지 |
| `TestValidCookieReturnsExistingAndIgnoresNewInput` | 유효 쿠키 반복 등록·다른 입력 → 200 기존 팀, 쿠키 발급 없음, 새 팀 없음 |
| `TestDiningCookieReturnsExisting` | 식사 중 fixture 쿠키 → 200 기존 팀. 쿠키 없는 같은 이메일 → 409 |
| `TestCompletedCookieConcurrentReRegisterCreatesOneParty` | 완료 fixture 쿠키로 서로 다른 이메일 동시 10건 → 201 한 건, 나머지 200은 같은 새 팀이고 쿠키 없음, 진행 중 팀 한 건, 201의 쿠키가 `successorId`의 팀을 가리킴 |
| `TestCompletedCookieConcurrentReRegisterBrowserJarEndsWithSuccessorCookie` | 하나의 쿠키 저장소를 공유하는 클라이언트(브라우저 흉내)로 동시 8건 × 5라운드 → 라운드마다 201 한 건, 최종 저장된 쿠키가 새 팀을 가리킴 |
| `TestStaleCookieAfterSuccessorCompletedActsAsNoCookie` | 재등록 팀까지 완료된 뒤 첫 완료 쿠키 → 쿠키 없는 요청처럼 처리(사용 중 이메일 409, 새 이메일 201), 첫 팀의 `successorId`는 바뀌지 않음 |
| `TestReRegisterSameEmailAfterCompletion` | 완료 후 같은 이메일 재등록(쿠키 없이 / 완료 쿠키로) 201, 새 쿠키가 새 팀을 가리킴, 오래된 쿠키 재요청은 200 |
| `TestInvalidCookieGrantsNoAccess` | 알 수 없는 토큰 + 사용 중 이메일 → 409(정보 없음); 알 수 없는/형식 오류 쿠키 + 새 이메일 → 201 |
| `TestFailedTransactionLeavesNoPartialChanges` | 완료 쿠키 재등록이 이메일 중복으로 실패 → `successorId`·순번·팀 모두 변경 없음 |
| `TestEnsureSchemaIsIdempotentAndKeepsState` | 스키마 준비 반복 실행 시 오류 없음, 기존 카운터·좌석 유지, 인덱스 3개(unique, 이메일은 partial) |
| `TestRegisterFailsCleanlyWithoutRestaurantDocument` | 식당 문서가 없으면 오류, 팀 생성 없음 |
| `TestTransactionTimeoutIsBounded` | 제한 시간을 넘기면 재시도하지 않고 실패, 팀 생성 없음 |
| `TestTransactionCommit`, `TestTransactionRollback` | 기존 트랜잭션 검증 유지 |

- 동시성 관련 테스트(`Concurrent`, `Completed`, `Failed`)는 `-count=10`으로 반복 실행해 모두 통과
- 커밋 전 검토(2026-11-17)에서 위 표의 동시 30건, 브라우저 쿠키 저장소, 오래된 쿠키 테스트를 추가·변경했고,
  `ConcurrentSameEmail|CompletedCookie|StaleCookie`를 `-count=10`으로 실행해 40회 모두 통과

### 정적 검사 — 통과

- `gofmt -l` 출력 없음, `go vet`(일반·integration 태그), `staticcheck v0.8.2`(일반·integration 태그)

### 실행 중인 컨테이너에서 확인 — 통과

- 앱 이미지 재빌드 후 시작 로그에 `database schema ready`, 앱 재시작 후에도 같은 로그(스키마 준비 반복 안전)
- `/healthz` 200, `/readyz` 200
- `POST /api/parties`: `Sec-Fetch-Site: cross-site` → 403, 다른 `Origin` → 403, 폼 Content-Type → 415, 잘못된 입력 → 400
- 영구 DB(`waitlist`)에는 인덱스 3개와 식당 문서 하나만 있고 팀 문서는 0건 (실제 등록 요청은 보내지 않음)

### 미실행

- Playwright E2E 전체 (화면 미구현)
- Go race detector (`go test -race`): 도구 이미지가 `CGO_ENABLED=0`인 alpine이라 실행하지 않음
- 실제 브라우저에서의 쿠키 저장·SameSite 동작 확인 (E2E 범위). 쿠키 저장소 테스트는 Go의 `net/http/cookiejar`로 흉내 낸 것이며 브라우저 동작 자체를 검증하지는 않음

---

## 상태 조회 API 검증 기록

- **실행일**: 2026-11-17
- **환경·데이터**: 등록 API 검증과 같음. 식사 중(`status`, `checkedInAt`, `serviceEndsAt`)·완료 상태와 사용 중 좌석은 DB fixture로 준비 (체크인·종료 기능은 구현하지 않음)
- **회귀**: 쿠키 해석을 공통 함수로 바꾸면서 등록 코드가 바뀌었으므로, 등록 API·health·트랜잭션 테스트를 포함한 전체 단위·통합 테스트를 다시 실행해 모두 통과

### 단위 테스트 — 통과

| 테스트 | 확인 내용 |
| --- | --- |
| `TestCanCheckIn` (`internal/party`) | 맨 앞·좌석 충분 → 가능, 딱 맞는 좌석 → 가능, 맨 앞·좌석 부족 → 불가, 뒤 팀은 좌석이 충분하거나 인원이 작아도 불가, 만석 → 불가 |
| `TestMeUnauthorizedWithoutValidCookie` | 쿠키 없음·형식 오류 → 401과 안내 문구, 저장소 조회 안 함 |
| `TestMeUnknownTokenIs401` | 미등록 토큰 → 401, 저장소에는 해시만 전달 |
| `TestMeInternalErrorHidesDetails` | 500 본문에 내부 상세 없음 |
| `TestMeResponseShapeAndHeaders` | 상태별 포함·생략 필드, 최상위 필드는 `party`/`waiting`/`dining`뿐, `Cache-Control: no-store`, `Set-Cookie` 없음, 토큰·해시 없음 |
| `TestMeOnlyGetIsRouted` | `POST /api/me` → 405 |

### 통합 테스트 (실제 MongoDB) — 통과

| 테스트 | 시나리오 |
| --- | --- |
| `TestMeWithIssuedCookie` | 등록 API가 발급한 실제 쿠키로 조회 200, 대기 중·앞 팀 0·체크인 가능, `no-store`, 쿠키 발급 없음, 이메일·토큰·해시·순번·`_id` 없음 |
| `TestMeUnauthorized` | 쿠키 없음·형식 오류·미등록 토큰 → 401, 다른 팀 정보 없음 |
| `TestMeAheadCountExcludesDiningAndCompleted` | 대기 4팀 중 1팀 식사 중·1팀 완료로 바꾸면 마지막 팀의 앞 팀 수가 3 → 1 |
| `TestMeCanCheckInRules` | 빈 좌석 4석: 맨 앞 5명 불가, 뒤 2명 불가(앞 팀 1). 빈 좌석 5석: 맨 앞 가능, 뒤 팀은 여전히 불가 |
| `TestMeDiningReturnsServiceEnd` | 식사 중 → 저장된 `serviceEndsAt` 그대로, `serverTime`은 요청 시각 근처, `waiting` 생략 |
| `TestMeCompletedReturns200` | 완료 → 200, `party`만 포함 |
| `TestMeFollowsSuccessorConsistentlyWithRegister` | 재등록 201의 쿠키를 버리고 이전 완료 쿠키만 남긴 상황: successor 대기 중·식사 중이면 조회는 successor 표시, 등록은 200; successor 완료 시 조회는 successor 완료(200), 등록은 새 팀 201; 새 팀은 새 쿠키로만 조회 |
| `TestBrokenSuccessorReferenceIsAnErrorNotUnregistered` | 끊어진 `successorId` → 조회 500·등록 500, 상세 없음, 새 팀·쿠키 없음, 데이터 변경 없음 |
| `TestMeDoesNotModifyAnything` | 대기·식사 중(종료 시각 지남)·완료 팀을 반복 조회해도 parties·restaurants 문서가 바이트 단위로 같음, 쿠키 발급 없음, 쿠키 계속 유효, 종료 시각이 지난 팀도 식사 중 유지 |
| `TestAheadCountQueryUsesWaitingIndex` (`internal/store`) | 앞 팀 수 조회의 실행 계획이 `sequence_waiting` 인덱스를 사용 |
| `TestStatusReadTimeoutIsBounded` (`internal/store`) | 조회 제한 시간을 넘기면 실패하고 2초 안에 반환 |
| `TestEnsureSchemaIsIdempotentAndKeepsState` (변경) | `sequence_waiting` 부분 인덱스 생성 확인 추가 |

- 새 테스트는 `-count=10`으로 반복 실행해 모두 통과
- `go test -v` 기준 `internal/api`, `internal/store`에서 PASS 45건, FAIL·SKIP 없음
- 실행 후 남은 임시 DB 없음

### 정적 검사 — 통과

- `gofmt -l` 출력 없음, `go vet`(일반·integration), `staticcheck v0.8.2`(일반·integration)

### 실행 중인 컨테이너에서 확인 — 통과

- 앱 재빌드 후 `/healthz` 200, `/readyz` 200
- `GET /api/me` 쿠키 없음 → 401, `Cache-Control: no-store`; 미등록 토큰 쿠키 → 401
- `POST /api/parties` cross-site → 403 (기존 CSRF 유지)
- 영구 DB(`waitlist`)에 `sequence_waiting` 인덱스가 추가됨, 팀 문서 0건 (실제 등록 요청은 보내지 않음)

### 미실행

- Playwright E2E, 화면 폴링 (미구현)
- 체크인 API가 `canCheckIn`과 무관하게 순서·좌석을 재검증하는지 → 아래 "체크인 API 검증 기록"에서 실행
- Go race detector (도구 이미지가 `CGO_ENABLED=0`)

---

## 체크인 API 검증 기록

- **실행일**: 2026-11-17
- **환경·데이터**: 이전 기록과 같음. 식사 중·완료 상태와 사용 중 좌석은 DB fixture로 준비 (식사 종료 작업은 구현하지 않음)
- **회귀**: 등록·상태 조회·health·트랜잭션 테스트를 포함한 전체 단위·통합 테스트를 다시 실행해 모두 통과

### 단위 테스트 — 통과

| 테스트 | 확인 내용 |
| --- | --- |
| `TestServiceDuration` (`internal/party`) | 1명 3초, 4명 12초, 10명 30초 |
| `TestCheckInSuccessResponse` | `checked_in`·`already_dining` 모두 200, `party`+`dining`+`result` 형식, `waiting` 없음, `no-store`, `Set-Cookie` 없음, 토큰·해시 없음, 저장소에는 해시만 전달 |
| `TestCheckInErrorMapping` | 미등록 401, `not_your_turn`·`not_enough_seats`·`already_completed` 409, 그 밖의 오류 500(상세 없음) |
| `TestCheckInWithoutValidCookieIs401` | 쿠키 없음·형식 오류 → 401, 저장소 호출 없음 |
| `TestCheckInRejectsBody` | `{}`, `{"partySize":1}`, `{"canCheckIn":true}`, 공백 → 400 `unexpected_body`, 저장소 호출 없음. 본문이 없으면 Content-Type과 관계없이 처리 |
| `TestCheckInCSRF` | same-origin·같은 Origin·비브라우저 허용, cross-site·same-site·다른 Origin → 403 (저장소 호출 없음) |
| `TestCheckInOnlyPostIsRouted` | `GET /api/me/check-in` → 405 |

### 통합 테스트 (실제 MongoDB) — 통과

| 테스트 | 시나리오 |
| --- | --- |
| `TestCheckInSuccessWithIssuedCookie` | 등록 API 쿠키로 체크인 200 `checked_in`, 저장: dining·체크인 시각(요청 시각 근처)·종료 = 체크인 + 4×3초, 응답 종료 시각 = 저장 값, 좌석 4, 이후 조회에서 A 식사 중·B 앞 팀 0·체크인 가능 |
| `TestCheckInUnauthorized` | 쿠키 없음·형식 오류·미등록 토큰 → 401, 데이터 변경 없음 |
| `TestCheckInBehindHeadIsRejectedEvenWithSeats` | 앞 팀(5명)이 있으면 1명 팀은 좌석이 충분해도 409 `not_your_turn`, 변경 없음 |
| `TestCheckInHeadWithoutEnoughSeatsLeavesNoPartialChanges` | 빈 좌석 4석에서 맨 앞 5명 → 409 `not_enough_seats`. 팀 갱신 뒤 좌석 확보에서 실패하는 경로인데 parties·restaurants가 바이트 단위로 그대로(부분 변경 없음). 좌석이 생기면 같은 요청 성공, 좌석 10 |
| `TestCheckInTenPartyInEmptyRestaurant` | 빈 식당에서 10명 팀 성공(좌석 10, 30초), 다음 팀은 맨 앞이지만 `not_enough_seats` |
| `TestCheckInRepeatedSequentiallyIsIdempotent` | 반복 요청은 `already_dining`, 좌석·저장 시각 불변 |
| `TestCheckInConcurrentSameParty` | 같은 팀 동시 15건 → `checked_in` 1건, 좌석 한 번만 차감, 모든 응답과 저장 값의 종료 시각 동일 |
| `TestCheckInAlreadyDiningReturnsStoredTimes` | 종료 시각이 지난 식사 중 fixture → 200 `already_dining`, 저장 시각 그대로, 완료 처리 안 함, 데이터 변경 없음 |
| `TestCheckInCompletedIsRejected` | 완료 → 409 `already_completed`, 재시작 없음 |
| `TestCheckInFollowsSuccessorRule` | 재등록 201 쿠키를 잃은 상황: 이전 완료 쿠키로 체크인하면 successor가 체크인(좌석 3), 이전 팀은 완료 유지. successor까지 완료되면 409, 변경 없음 |
| `TestCheckInBrokenSuccessorIsInternalError` | 끊어진 `successorId` → 500, 상세 없음, 변경 없음 |
| `TestCheckInCSRFRejectedChangesNothing` | cross-site → 403, 변경 없음 |
| `TestCheckInConcurrentDifferentPartiesKeepsOrderAndCapacity` | 구성 4가지(4·3·3·2·5·1 / 5·5·1 / 1×12 / 6·5·1)로 대기 팀 전체가 동시에 요청하는 라운드를 진행이 멈출 때까지 반복. 매 라운드 응답은 200·409뿐, 식사 중 팀은 항상 등록 순서의 앞부분, 좌석 = 식사 중 인원 합 ≤ 10. 최종 식사 중 팀 수는 각각 3·2·10·1이고, 남은 맨 앞 팀은 `not_enough_seats`, 그 뒤 팀은 `not_your_turn` |

- 체크인 테스트 전체를 `-count=10`으로 반복 실행해 모두 통과
- 실행 후 남은 임시 DB 없음

### 정적 검사 — 통과

- `gofmt -l` 출력 없음, `go vet`(일반·integration), `staticcheck v0.8.2`(일반·integration)

### 실행 중인 컨테이너에서 확인 — 통과

- 앱 재빌드 후 `/healthz` 200, `/readyz` 200, `GET /api/me`(쿠키 없음) 401
- `POST /api/me/check-in`: 쿠키 없음 → 401 + `no-store`, 미등록 토큰 → 401, 본문 있음 → 400, cross-site → 403
- 영구 DB(`waitlist`)는 팀 0건, `seatsInUse` 0 그대로 (실제 등록·체크인은 하지 않음)

### 미실행

- Playwright E2E, 화면의 체크인 버튼·폴링 (미구현)
- 식사 종료 후 좌석 반환과 다음 팀 체크인 → 아래 "식사 종료 작업 검증 기록"에서 실행
- Go race detector (도구 이미지가 `CGO_ENABLED=0`)

---

## 식사 종료 작업 검증 기록

- **실행일**: 2026-11-17
- **환경·데이터**: 이전 기록과 같음. 식사 중 상태·종료 시각·좌석은 DB fixture 또는 실제 체크인 API로 준비
- **시간 제어**: 종료 경계는 `CompleteDue`에 넘기는 `now`로 제어해 실제로 기다리지 않음. 실제 주기 실행 테스트만 300ms~3초 안에서 대기
- **회귀**: 등록·상태 조회·체크인·health·트랜잭션 테스트를 포함한 전체 단위·통합 테스트를 다시 실행해 모두 통과 (`go test -v` 최상위 PASS 99건, FAIL·SKIP 없음)

### 단위 테스트 (`internal/worker`, 가짜 저장소) — 통과

| 테스트 | 확인 내용 |
| --- | --- |
| `TestSweepsImmediatelyOnStart` | 간격이 1시간이어도 시작 직후 한 번 순회, 제어 가능한 시계(`Now`) 값 전달 |
| `TestSweepsRepeatWithoutOverlap` | 순회(30ms)가 간격(5ms)보다 길어도 동시에 실행되는 순회는 최대 1개 |
| `TestRetriesAfterTransientError` | 첫 순회 오류 → "will retry" 기록, 다음 순회에서 처리 |
| `TestStopsOnContextCancelDuringSweep` | 진행 중인 순회가 ctx 취소로 멈추고 `Run`이 반환. 종료 취소를 재시도 오류로 기록하지 않음 |
| `TestSweepTimeoutBoundsOneSweep` | 끝나지 않는 순회가 제한 시간으로 끊기고 다음 순회가 이어짐 |
| `TestFailuresAreLoggedWithoutPersonalData` | 팀별 실패를 문서 ID와 원인으로 기록, 로그에 이메일·토큰 없음 |

### 통합 테스트 (실제 MongoDB) — 통과

| 테스트 | 시나리오 |
| --- | --- |
| `TestCompleteDueRespectsEndTimeBoundary` | 종료 1ms 전: 식사 중·좌석 유지. 종료 시각과 같으면: 완료, 좌석 정확히 반환 |
| `TestCompleteDueOnlyTouchesDueDiningParties` | 종료된 팀만 완료(좌석 7 → 4). 아직 종료 전 팀과 대기 팀은 바이트 단위로 그대로. 완료 팀은 삭제되지 않고 `tokenHash`·`successorId`·시각·순번 그대로. 대기 팀 자동 체크인 없음 |
| `TestCompleteDueRepeatedAndConcurrentReturnsSeatsOnce` | 동시 12회 + 순차 3회 실행 → 완료 합계 2건, 좌석 9 → 1 |
| `TestCompleteDueUnderflowIsRolledBackAndDoesNotBlockOthers` | 좌석이 음수가 될 팀: 상태 변경까지 취소(바이트 단위로 그대로), 좌석 보정 없음, 실패로 보고. `batch=1`인데도 같은 순회에서 뒤의 정상 팀은 완료. 다음 순회에서도 같은 실패, 좌석 불변 |
| `TestCompleteDueRecoversAfterError` | 식당 문서가 없어 실패 → 팀 변경 없음. 문서를 복구하면 다음 순회에서 완료 |
| `TestCompleteDuePaginatesAllDueParties` | 같은 종료 시각을 포함한 7팀을 `batch=2`로 나눠 읽어 모두 완료 |
| `TestCompleteDueStopsOnCancelledContext` | 취소된 ctx → 오류, 변경 없음 |
| `TestDueQueryUsesDiningEndsIndex` | 종료 대상 조회의 실행 계획이 `serviceEndsAt_dining` 인덱스를 사용 |
| `TestEnsureSchemaIsIdempotentAndKeepsState` (변경) | `serviceEndsAt_dining` 부분 인덱스 생성 확인 추가 |
| `TestAfterServiceEndStatusSeatsAndReRegistration` | 실제 체크인 후 종료 1ms 전에는 변화 없음, 종료 시각에 완료·좌석 0. `GET /api/me` 완료 상태, 다음 팀 `canCheckIn` 거짓 → 참(자동 체크인 없음), 완료 팀 체크인 409, 같은 이메일 재등록 201 |
| `TestConcurrentCheckInAndServiceEndKeepSeatsConsistent` | 10팀에 대해 체크인 4개 고루틴과 종료 순회 2개 고루틴을 동시에 반복. 관찰 중 좌석이 0~10을 벗어난 적 없음, 정지 후 좌석 = 식사 중 인원 합, 최종적으로 10팀 모두 완료·좌석 0 |
| `TestWorkerCompletesOverdueOnStartup` | 시작 직후 순회로 이미 지난 팀 완료, 아직 종료 전 팀 유지 |
| `TestWorkerCompletesWithoutRequests` | HTTP 요청 없이 실제 주기 실행만으로 종료 시각이 된 팀 완료, 좌석 0, 로그에 이메일 없음 |
| `TestWorkerStopsOnCancel` | 정지 후에는 종료 대상이 생겨도 처리하지 않음 |

- 식사 종료·동시성·작업 관련 테스트를 `-count=10`으로 반복 실행해 모두 통과
- 실행 후 남은 임시 DB 없음

### 정적 검사 — 통과

- `gofmt -l` 출력 없음, `go vet`(일반·integration), `staticcheck v0.8.2`(일반·integration), `docker compose config -q`

### 실행 중인 컨테이너에서 확인 — 통과

- 앱 재빌드 후 로그 순서: `database schema ready` → `http server listening` → `service-end worker started`
- `docker compose restart app`: `shutdown signal received` → `service-end worker stopped` → 다시 시작 (작업이 멈춘 뒤 프로세스 종료)
- `/healthz` 200, `/readyz` 200(재시작 후 포함), `GET /api/me`·`POST /api/me/check-in` 쿠키 없음 → 401
- 영구 DB(`waitlist`)에 `serviceEndsAt_dining` 인덱스 추가, 팀 0건, 좌석 0 (실제 등록·체크인은 하지 않음)

### 미실행

- Playwright E2E, 화면의 남은 시간 표시·폴링 → 아래 "손님 화면 E2E 검증 기록"에서 실행
- 실제 서버 프로세스를 식사 중에 멈췄다가 종료 시각 뒤에 다시 켜는 시험 (영구 DB에 데이터를 만들지 않기 위해 생략. 같은 동작을 작업 단위 테스트로 확인)
- 여러 서버 인스턴스 실행 (동시 순회 테스트로 같은 조건을 확인)
- Go race detector (도구 이미지가 `CGO_ENABLED=0`)

---

## 손님 화면 E2E 검증 기록

- **실행일**: 2026-11-17
- **환경**: `scripts/e2e.sh` — Compose `e2e` profile. 개발용 `app`과 분리된 `app-e2e`(같은 이미지)를 임시 DB `e2e_<시각>_<난수>`로 실행하고,
  공식 Playwright 이미지 `mcr.microsoft.com/playwright:v1.65.1-noble`(Chromium)에서 `@playwright/test 1.65.1`로 실행. 두 컨테이너 모두 호스트 포트를 공개하지 않음
- **데이터**: 테스트마다 임시 DB의 팀 문서를 지우고 좌석·순번을 초기화(같은 식당 대기열을 쓰므로 하나씩 실행). 실행 후 `app-e2e` 컨테이너와 임시 DB만 삭제. 실행 후 남은 `e2e_` DB 없음, 개발 DB `waitlist`는 팀 0건·좌석 0 그대로임을 확인
- **결과**: 33개 테스트(데스크톱 32 + 모바일 1)를 **연속 3회 실행해 모두 통과** (각 1.3~1.4분)
- **회귀**: Go 단위·통합 테스트 전체 재실행 통과, `gofmt`·`go vet`·`staticcheck` 통과, `node --check`로 JS 문법 확인

### 실제 서버·DB를 쓴 것과 흉내 낸 것

| 방식 | 사용한 곳 |
| --- | --- |
| 실제 화면·API·DB·식사 종료 작업 | 정상 등록·체크인·식사 완료·재등록, 두·세 브라우저 대기열, 좌석 반환 후 버튼 표시, 400(서버 검증), 409(이메일 중복), 401(쿠키 삭제), 체크인 409(요청 직전 좌석을 DB에서 채움), 끊어진 successor 참조 500(DB fixture), 브라우저 재시작 복원 |
| 서버의 실제 응답을 대신 전달 | 403: 브라우저는 `Sec-Fetch-Site`를 바꿀 수 없어서, API 요청으로 서버의 실제 CSRF 거부 응답을 받아 브라우저 요청의 응답으로 전달 |
| 네트워크·응답 흉내 (Playwright 경로 가로채기) | 연결 실패(abort), 조회 500, 등록 500, 체크인 응답 유실(서버 처리 후 응답만 끊음), 체크인 미전송, 늦게 도착한 조회 응답, 이전 단계 응답, 종료 시각이 지난 식사 중 응답 |
| 브라우저 상태 흉내 | 탭 숨김·복귀(`visibilityState`와 `visibilitychange` 이벤트를 페이지 안에서 바꿈) |

### 테스트별 결과 (모두 통과)

| 파일 | 테스트 | 확인 내용 |
| --- | --- | --- |
| `ssr.spec.js` | 초기 HTML(등록) | `/join` HTML에 label·입력·1~10 제한·noscript, `no-store` |
| | 초기 HTML(상태) | 쿠키가 있는 `/status` HTML에 단계·"김하나 · 2명"·"입장 차례입니다"·초기 상태 JSON |
| | 미등록 페이지 | 안내 문구와 `/join` 링크 |
| | JavaScript 안내 | 두 페이지 HTML의 `<noscript>` 안내 |
| | JavaScript 꺼짐 | 내용은 보이고 등록·체크인 버튼은 비활성화 |
| | 첫 화면 안내 | `/` → 미등록이면 `/join`, 등록 후에는 `/status` |
| `flow.spec.js` | 전체 흐름 | 화면 등록 → 진행 중 `/join` 접근 시 `/status` → 체크인 → 남은 시간 1~3초 → 서버 완료 → 완료 화면, 완료 후 조회 0회 → "다시 대기 등록하기" → 같은 이메일(대소문자 다름) 재등록 |
| `join.spec.js` | 브라우저 입력 검사 | 빈 값·11명을 서버 요청 없이 필드 옆에 안내, 첫 오류 필드 포커스 |
| | 서버 400 | `a@b`(브라우저는 허용, 서버는 거부) → 서버 문구 표시, 입력값 유지, 수정 후 등록 |
| | 409 | 다른 브라우저의 같은 이메일(공백·대소문자 다름) → 중복 안내, 다른 팀 정보 없음 |
| | 403·연결 실패·500 | 각각의 안내, 입력값 유지, 같은 입력으로 재시도해 등록 |
| | 중복 제출 방지 | 처리 중 버튼 비활성화("등록하는 중…"), 클릭·Enter 반복에도 요청 1건·팀 1건 |
| `queue.spec.js` | 두 브라우저 선착순 | 앞 팀 수 자동 갱신, 서버 렌더링 문구 = 갱신 후 문구, 앞 팀 체크인 후 뒤 팀 버튼 자동 표시 |
| | 맨 앞 팀 대기·좌석 반환 | 4명 식사 중, 7명(맨 앞) "다음 순서입니다", 1명은 좌석이 충분해도 "내 앞에 1팀"·버튼 없음 → 4명 식사 종료 후 7명 화면에 버튼 자동 표시, 1명은 계속 대기 |
| `resilience.spec.js` | 조회 연결 실패·복구 | 연결 안내("연결되면 체크인할 수 있습니다"), 마지막 상태 유지, 버튼 비활성화 → 복구 후 안내 사라지고 버튼 활성화 |
| | 조회 500 | 오류 안내, 완료·미등록으로 바뀌지 않음, 복구 |
| | 401 | 쿠키 삭제 후 미등록 화면, "대기 등록하기"로 `/join` 이동 |
| | 끊어진 참조 | `/status`·`/join`이 500 오류 화면, 미등록 문구·등록 폼 없음 |
| | 체크인 409 | 좌석 부족 사유 표시, 대기 화면으로 돌아가도 사유 유지 → 좌석이 생기면 버튼 재표시 후 체크인 성공 |
| | 체크인 403 | 새로고침 안내, 상태 변화 없음 |
| | 체크인 응답 유실 | 서버는 처리, 응답만 끊김 → 실패로 단정하지 않고 조회로 식사 중 반영 |
| | 체크인 미전송 | 조회로 확인 후 "체크인이 처리되지 않았습니다" → 다시 눌러 성공 |
| | 응답 순서 | 체크인 전에 보낸 조회 응답을 체크인 후에 전달해도 식사 화면 유지, 같은 팀의 대기 응답이 와도 식사 화면 유지 |
| | 카운트다운 0 | 종료 시각이 지난 식사 중 응답 → "이용 완료 확인 중", 완료로 바꾸지 않음 → 실제 응답으로 완료 화면 |
| `lifecycle.spec.js` | 탭 숨김·복귀 | 숨김 중 조회 0회·화면 갱신 없음 → 복귀 즉시 조회·체크인 가능 반영 |
| | 뒤로가기·앞으로가기 | 다른 페이지에 다녀오는 동안의 변화 반영, 이후 조회 계속(2.5초에 2회 이상), 다시 앞으로·뒤로 후에도 동일 |
| | 브라우저 재시작 | 디스크 프로필로 브라우저 종료 후 재실행 → 상태 복원, 다른 프로필은 미등록 |
| `a11y.spec.js` | 키보드 | Tab·입력·Enter만으로 등록, 체크인 버튼까지 Tab 후 Enter로 체크인, 포커스 외곽선 표시 |
| | 오류 연결 | `aria-invalid`, `aria-describedby`, 접근성 설명에 오류 문구 |
| | 이스케이프 | 이름 `<img src=x onerror=…><b>굵게</b>`가 서버 렌더링·갱신 후 모두 문자 그대로, 실제 요소 생성·스크립트 실행 없음 |
| | 알림 범위 | 남은 시간은 알림 영역 밖, 체크인 후 알림 영역에 상태 변화 문구 |
| `layout.spec.js` (데스크톱·모바일 Pixel 7) | 화면 배치 | 등록·체크인 가능·식사 중 화면에서 가로 스크롤 없음, 버튼 높이 44px 이상, 입력 글자 16px 이상, 긴 이름 줄바꿈. 스크린샷을 실행 결과 폴더에 저장해 눈으로도 확인 |

### 미실행·한계

- Chromium만 실행. Firefox·Safari(WebKit)와 실제 휴대폰은 확인하지 않음 (모바일은 Pixel 7 에뮬레이션)
- 실제 브라우저 탭 전환·백그라운드 타이머 지연은 헤드리스에서 재현할 수 없어 `visibilitychange`를 흉내 냄
- Playwright의 JavaScript 비활성화는 `<noscript>` 내용을 그리지 않으므로, 안내 문구는 HTML 수준에서만 확인
- 화면 읽기 프로그램(스크린리더)으로 실제 낭독을 듣는 확인은 하지 않음. 접근성 트리·속성으로만 확인
- 뒤로가기 캐시(bfcache) 복원 여부는 브라우저 판단이라, 복원·재요청 어느 경우든 최신 상태와 조회 재개를 확인하는 방식으로 검증
- Go race detector (도구 이미지가 `CGO_ENABLED=0`)
