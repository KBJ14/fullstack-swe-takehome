// Package worker는 서버 내부에서 주기적으로 실행하는 작업을 담는다.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
)

// 식사 종료 작업 기본값.
const (
	// DefaultInterval은 확인 간격이다. (초기값 1초, docs/design.md 2.6)
	DefaultInterval = time.Second
	// DefaultSweepTimeout은 순회 한 번에 허용하는 전체 시간이다.
	// 팀마다의 트랜잭션은 store의 쓰기 트랜잭션 제한(5초)을 따로 받는다.
	DefaultSweepTimeout = 10 * time.Second
)

// Completer는 종료 시각이 지난 식사 중 팀을 완료 처리한다. (store.Store가 구현)
type Completer interface {
	CompleteDue(ctx context.Context, now time.Time, batch int) (store.SweepReport, error)
}

// ServiceEnd는 식사 종료·좌석 반환 작업이다.
//
//   - Run을 호출한 고루틴 하나에서 순회를 차례로 실행하므로, 같은 프로세스에서 순회가 겹치지 않는다.
//     순회가 간격보다 오래 걸리면 밀린 틱은 버려지고 다음 틱에 이어서 실행한다.
//   - 시작 직후 한 번 바로 순회해, 서버가 멈춰 있던 동안 종료 시각이 지난 팀도 처리한다.
//   - 순회 오류(일시적 DB 오류 등)는 기록만 하고 다음 순회에서 다시 시도한다.
//   - 로그에는 팀 문서 ID와 개수만 남긴다. 토큰·이메일·이름은 남기지 않는다.
type ServiceEnd struct {
	Store        Completer
	Logger       *slog.Logger
	Interval     time.Duration
	SweepTimeout time.Duration
	Batch        int
	// Now는 현재 시각이다. 비어 있으면 time.Now. 테스트에서 시계를 제어할 때 바꾼다.
	Now func() time.Time
}

// Run은 ctx가 취소될 때까지 순회를 반복한다. 진행 중인 순회도 ctx 취소로 멈춘다.
// 반환되면 더 이상 DB를 사용하지 않으므로, 호출자는 그 뒤에 DB 연결을 닫으면 된다.
func (w *ServiceEnd) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	w.Logger.Info("service-end worker started", "interval", interval.String())
	defer w.Logger.Info("service-end worker stopped")

	w.sweep(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep은 순회 한 번이다.
func (w *ServiceEnd) sweep(parent context.Context) {
	if parent.Err() != nil {
		return
	}
	timeout := w.SweepTimeout
	if timeout <= 0 {
		timeout = DefaultSweepTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	now := time.Now
	if w.Now != nil {
		now = w.Now
	}

	report, err := w.Store.CompleteDue(ctx, now(), w.Batch)
	for _, f := range report.Failures {
		w.Logger.Error("service-end failed for party", "partyId", f.PartyID, "error", f.Err)
	}
	if report.Completed > 0 {
		w.Logger.Info("service ended", "completed", report.Completed, "skipped", report.Skipped)
	}
	if err != nil && parent.Err() == nil {
		// 서버 종료로 인한 취소가 아니면 기록하고 다음 순회에서 다시 시도한다.
		w.Logger.Warn("service-end sweep incomplete, will retry", "error", err)
	}
}
