// Package party는 대기 팀의 도메인 규칙(상태, 등록 입력 검증, 등록 결과)을 정의한다.
//
// 이 패키지는 DB나 HTTP에 의존하지 않는 순수 규칙만 담는다.
package party

import (
	"context"
	"errors"
	"time"
)

// Status는 DB에 저장하는 팀 상태다.
//
// "체크인 가능"은 저장하지 않고 계산하는 값이므로 여기에 없다. (docs/design.md 2.4)
type Status string

const (
	StatusWaiting   Status = "waiting"   // 대기 중
	StatusDining    Status = "dining"    // 식사 중
	StatusCompleted Status = "completed" // 이용 완료
)

// Active는 같은 이메일의 추가 등록을 막아야 하는 진행 중 상태인지 알려 준다.
func (s Status) Active() bool {
	return s == StatusWaiting || s == StatusDining
}

// ActiveStatuses는 진행 중 상태 목록이다. DB의 부분 고유 인덱스 조건과 같아야 한다.
var ActiveStatuses = []Status{StatusWaiting, StatusDining}

// Outcome은 등록 요청의 처리 결과 종류다.
type Outcome string

const (
	// OutcomeCreated는 새 팀을 만들었다는 뜻이다. 새 식별 쿠키를 발급한다.
	OutcomeCreated Outcome = "created"
	// OutcomeExisting은 쿠키가 가리키는 진행 중 등록이 있어 새 팀을 만들지 않았다는 뜻이다.
	OutcomeExisting Outcome = "existing"
)

// View는 등록 응답에 담는 최소 팀 정보다.
//
// 이메일, 등록 순번, 토큰 검증값은 담지 않는다.
// 체크인 가능 여부와 앞 팀 수는 아직 구현하지 않았으므로 담지 않는다.
type View struct {
	Name         string    `json:"name"`
	PartySize    int       `json:"partySize"`
	Status       Status    `json:"status"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// RegisterResult는 등록 처리 결과다.
type RegisterResult struct {
	Outcome Outcome
	Party   View
}

// ErrEmailInUse는 같은 정규화 이메일로 진행 중인 등록이 이미 있을 때 반환된다.
var ErrEmailInUse = errors.New("email already has an active registration")

// RegisterCommand는 저장소에 전달하는 등록 요청이다.
//
// 저장소는 토큰 원문을 받지 않고 검증용 해시만 받는다.
type RegisterCommand struct {
	Input Input
	// PresentedTokenHash는 요청 쿠키의 토큰 해시다. 쿠키가 없거나 형식이 잘못되면 빈 값이다.
	PresentedTokenHash string
	// NewTokenHash는 새 팀을 만들 때 저장할 토큰 해시다.
	NewTokenHash string
}

// Registrar는 등록 규칙을 저장소에 적용한다.
type Registrar interface {
	Register(ctx context.Context, cmd RegisterCommand) (RegisterResult, error)
}
