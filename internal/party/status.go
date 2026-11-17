package party

import (
	"context"
	"errors"
	"time"
)

// ErrNotRegistered는 쿠키가 없거나 형식이 잘못됐거나, 토큰에 연결된 팀이 없을 때 반환된다.
var ErrNotRegistered = errors.New("no registration for this token")

// StatusView는 GET /api/me 응답의 내용이다. (docs/design.md 2.7)
//
// 상태별 포함 규칙
//   - Party: 항상 포함
//   - Waiting: 상태가 waiting일 때만 포함
//   - Dining: 상태가 dining일 때만 포함
//   - completed: Party만 포함
type StatusView struct {
	Party   View         `json:"party"`
	Waiting *WaitingInfo `json:"waiting,omitempty"`
	Dining  *DiningInfo  `json:"dining,omitempty"`
}

// WaitingInfo는 대기 중인 팀의 대기열 정보다.
type WaitingInfo struct {
	// AheadCount는 내 등록 순번보다 앞선 대기 중 팀의 수다. 식사 중·완료 팀은 세지 않는다.
	AheadCount int64 `json:"aheadCount"`
	// CanCheckIn은 지금 체크인할 수 있는지에 대한 안내다. 저장하지 않고 계산한다.
	// 체크인 API는 이 값을 믿지 않고 순서와 좌석을 다시 확인해야 한다.
	CanCheckIn bool `json:"canCheckIn"`
}

// DiningInfo는 식사 중인 팀의 시간 정보다.
type DiningInfo struct {
	// ServiceEndsAt은 서버가 저장한 식사 종료 예정 시각이다.
	ServiceEndsAt time.Time `json:"serviceEndsAt"`
	// ServerTime은 응답을 만든 시점의 서버 시각이다.
	// 화면은 (ServiceEndsAt - ServerTime)으로 남은 시간을 계산하면 브라우저 시계 오차의 영향을 받지 않는다.
	ServerTime time.Time `json:"serverTime"`
}

// StatusReader는 토큰 해시로 팀의 현재 상태를 조회한다.
type StatusReader interface {
	Status(ctx context.Context, tokenHash string) (StatusView, error)
}

// CanCheckIn은 대기 중인 팀이 지금 체크인할 수 있는지 계산한다.
//
// 조건: 내 앞의 대기 팀이 없고(등록 순서 엄수), 빈 좌석(정원 - 사용 중 좌석)이 내 인원 이상.
// 맨 앞 팀이 좌석 부족으로 기다리면 뒤 팀은 인원이 작아도 체크인할 수 없다.
func CanCheckIn(aheadCount int64, capacity, seatsInUse, partySize int) bool {
	return aheadCount == 0 && capacity-seatsInUse >= partySize
}
