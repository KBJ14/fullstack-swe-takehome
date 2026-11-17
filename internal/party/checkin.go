package party

import (
	"context"
	"errors"
	"time"
)

// ServicePerPerson은 인원 한 명당 식사 시간이다. (원문 요구사항: 3초 고정)
const ServicePerPerson = 3 * time.Second

// ServiceDuration은 팀의 식사 시간(인원 × 3초)이다.
func ServiceDuration(partySize int) time.Duration {
	return time.Duration(partySize) * ServicePerPerson
}

// 체크인 거부 사유. 모두 HTTP 409로 응답한다.
var (
	// ErrNotYourTurn은 내 앞에 아직 대기 중인 팀이 있을 때 반환된다. (좌석이 충분해도 추월 불가)
	ErrNotYourTurn = errors.New("party is not at the head of the queue")
	// ErrNotEnoughSeats는 맨 앞 팀이지만 빈 좌석이 인원보다 적을 때 반환된다.
	ErrNotEnoughSeats = errors.New("not enough free seats")
	// ErrAlreadyCompleted는 이미 이용을 마친 팀이 체크인을 요청할 때 반환된다. 식사를 다시 시작하지 않는다.
	ErrAlreadyCompleted = errors.New("party has already completed service")
)

// CheckInOutcome은 체크인 요청의 결과 종류다.
type CheckInOutcome string

const (
	// CheckInStarted는 이번 요청으로 체크인해 식사를 시작했다는 뜻이다.
	CheckInStarted CheckInOutcome = "checked_in"
	// CheckInAlreadyDining은 이미 식사 중이라 아무것도 바꾸지 않았다는 뜻이다.
	CheckInAlreadyDining CheckInOutcome = "already_dining"
)

// CheckInResult는 체크인 결과다. View는 GET /api/me와 같은 형식이며 항상 식사 중 상태다.
type CheckInResult struct {
	Outcome CheckInOutcome
	View    StatusView
}

// CheckInner는 토큰 해시가 가리키는 팀을 체크인한다.
//
// 클라이언트는 팀 ID·인원·시각을 지정하지 않는다. 모두 서버가 DB에서 다시 확인한다.
type CheckInner interface {
	CheckIn(ctx context.Context, tokenHash string) (CheckInResult, error)
}
