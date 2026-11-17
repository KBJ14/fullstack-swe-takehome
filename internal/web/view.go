package web

import (
	"fmt"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

// 화면 단계. 서버(SSR)와 static/status.js의 phaseOf가 같은 규칙을 쓴다.
//
//	waiting      대기 중이고 아직 체크인할 수 없음
//	ready        대기 중이고 체크인 가능 (canCheckIn)
//	dining       식사 중
//	completed    이용 완료
//	unregistered 쿠키가 없거나 연결된 팀이 없음
const (
	phaseWaiting      = "waiting"
	phaseReady        = "ready"
	phaseDining       = "dining"
	phaseCompleted    = "completed"
	phaseUnregistered = "unregistered"
)

// phaseOf는 상태 조회 결과로 화면 단계를 정한다. (static/status.js의 phaseOf와 같아야 함)
func phaseOf(v *party.StatusView) string {
	if v == nil {
		return phaseUnregistered
	}
	switch v.Party.Status {
	case party.StatusWaiting:
		if v.Waiting != nil && v.Waiting.CanCheckIn {
			return phaseReady
		}
		return phaseWaiting
	case party.StatusDining:
		return phaseDining
	default:
		return phaseCompleted
	}
}

// aheadText는 대기 중 화면의 큰 글씨 문구다. (static/status.js의 aheadText와 같아야 함)
func aheadText(n int64) string {
	if n > 0 {
		return fmt.Sprintf("내 앞에 %d팀", n)
	}
	return "다음 순서입니다"
}

// hintText는 대기 중 화면의 보조 문구다. (static/status.js의 hintText와 같아야 함)
func hintText(n int64) string {
	if n > 0 {
		return "앞 팀이 입장하면 순서가 줄어듭니다."
	}
	return "빈 좌석이 생기면 체크인 버튼이 나타납니다."
}

// remainingText는 남은 식사 시간 문구다. (static/status.js의 remainingText와 같아야 함)
//
// 남은 시간은 올림한 초 단위로 보여 준다. 0 이하가 돼도 화면이 완료로 바꾸지 않고,
// 서버가 완료 처리할 때까지 "이용 완료 확인 중"으로 표시한다.
func remainingText(d time.Duration) string {
	if d <= 0 {
		return "이용 완료 확인 중"
	}
	s := int64((d + time.Second - 1) / time.Second)
	if m := s / 60; m > 0 {
		return fmt.Sprintf("%d분 %02d초", m, s%60)
	}
	return fmt.Sprintf("%d초", s)
}

// partyLine은 상단의 "이름 · N명" 문구다. (static/status.js와 같아야 함)
func partyLine(v *party.StatusView) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%s · %d명", v.Party.Name, v.Party.PartySize)
}

// statusPage는 내 상태 화면의 템플릿 데이터다.
type statusPage struct {
	Phase     string
	PartyLine string
	Ahead     string
	Hint      string
	Remaining string
	// State는 초기 상태다. 화면 스크립트가 같은 형식(GET /api/me 응답)으로 이어서 갱신한다.
	State *party.StatusView
}

func newStatusPage(v *party.StatusView) statusPage {
	p := statusPage{Phase: phaseOf(v), PartyLine: partyLine(v), State: v}
	if v != nil && v.Waiting != nil {
		p.Ahead, p.Hint = aheadText(v.Waiting.AheadCount), hintText(v.Waiting.AheadCount)
	}
	if v != nil && v.Dining != nil {
		p.Remaining = remainingText(v.Dining.ServiceEndsAt.Sub(v.Dining.ServerTime))
	}
	return p
}
