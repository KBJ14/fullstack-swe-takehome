package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
)

// fakeCompleter는 호출을 기록하고, 정해 둔 동작을 순서대로 수행한다.
type fakeCompleter struct {
	mu       sync.Mutex
	calls    int
	inFlight atomic.Int32
	maxIn    atomic.Int32
	nows     []time.Time
	// behave는 호출 번호(0부터)별 동작이다. 비어 있으면 즉시 성공.
	behave func(ctx context.Context, n int) (store.SweepReport, error)
}

func (f *fakeCompleter) CompleteDue(ctx context.Context, now time.Time, _ int) (store.SweepReport, error) {
	in := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxIn.Load()
		if in <= m || f.maxIn.CompareAndSwap(m, in) {
			break
		}
	}
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.nows = append(f.nows, now)
	f.mu.Unlock()
	if f.behave != nil {
		return f.behave(ctx, n)
	}
	return store.SweepReport{}, nil
}

func (f *fakeCompleter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func waitFor(t *testing.T, cond func() bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSweepsImmediatelyOnStart(t *testing.T) {
	f := &fakeCompleter{}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixed := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	go (&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: time.Hour, Now: func() time.Time { return fixed }}).Run(ctx)

	// 간격이 1시간이어도 시작 직후 한 번 실행된다.
	waitFor(t, func() bool { return f.callCount() >= 1 }, time.Second)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.nows[0].Equal(fixed) {
		t.Fatalf("worker must pass the controllable clock: %v", f.nows[0])
	}
}

func TestSweepsRepeatWithoutOverlap(t *testing.T) {
	// 순회 하나가 간격보다 오래 걸려도 다음 순회가 겹쳐 시작하지 않는다.
	f := &fakeCompleter{behave: func(ctx context.Context, _ int) (store.SweepReport, error) {
		select {
		case <-time.After(30 * time.Millisecond):
		case <-ctx.Done():
		}
		return store.SweepReport{}, nil
	}}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: 5 * time.Millisecond}).Run(ctx)
	}()
	waitFor(t, func() bool { return f.callCount() >= 4 }, 2*time.Second)
	cancel()
	<-done
	if m := f.maxIn.Load(); m != 1 {
		t.Fatalf("sweeps overlapped: max in flight %d", m)
	}
}

func TestRetriesAfterTransientError(t *testing.T) {
	f := &fakeCompleter{behave: func(_ context.Context, n int) (store.SweepReport, error) {
		if n == 0 {
			return store.SweepReport{}, errors.New("server selection timeout")
		}
		return store.SweepReport{Completed: 2}, nil
	}}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: 10 * time.Millisecond}).Run(ctx)
	}()
	waitFor(t, func() bool { return f.callCount() >= 2 }, 2*time.Second)
	cancel()
	<-done
	logs := buf.String()
	if !strings.Contains(logs, "will retry") || !strings.Contains(logs, "service ended") {
		t.Fatalf("expected retry and recovery in logs:\n%s", logs)
	}
}

func TestStopsOnContextCancelDuringSweep(t *testing.T) {
	started := make(chan struct{})
	f := &fakeCompleter{behave: func(ctx context.Context, n int) (store.SweepReport, error) {
		if n == 0 {
			close(started)
		}
		<-ctx.Done() // 오래 걸리는 순회: 취소될 때까지 기다린다
		return store.SweepReport{}, ctx.Err()
	}}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: time.Hour, SweepTimeout: time.Hour}).Run(ctx)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if strings.Contains(buf.String(), "will retry") {
		t.Fatal("shutdown cancellation must not be logged as a retryable failure")
	}
	if !strings.Contains(buf.String(), "service-end worker stopped") {
		t.Fatal("missing stop log")
	}
}

func TestSweepTimeoutBoundsOneSweep(t *testing.T) {
	f := &fakeCompleter{behave: func(ctx context.Context, n int) (store.SweepReport, error) {
		<-ctx.Done()
		return store.SweepReport{}, ctx.Err()
	}}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go (&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: 10 * time.Millisecond, SweepTimeout: 20 * time.Millisecond}).Run(ctx)
	// 순회가 제한 시간으로 끝나므로 다음 순회가 이어서 실행된다.
	waitFor(t, func() bool { return f.callCount() >= 3 }, 2*time.Second)
}

func TestFailuresAreLoggedWithoutPersonalData(t *testing.T) {
	f := &fakeCompleter{behave: func(_ context.Context, n int) (store.SweepReport, error) {
		return store.SweepReport{Completed: 1, Failures: []store.SweepFailure{
			{PartyID: "65f0c0ffee", Err: errors.New("returning seats would make seatsInUse negative: party size 4")},
		}}, nil
	}}
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&ServiceEnd{Store: f, Logger: newLogger(&buf), Interval: time.Hour}).Run(ctx)
	}()
	waitFor(t, func() bool { return strings.Contains(buf.String(), "service ended") }, time.Second)
	cancel()
	<-done
	logs := buf.String()
	if !strings.Contains(logs, "partyId=65f0c0ffee") || !strings.Contains(logs, "seatsInUse negative") {
		t.Fatalf("failure not logged:\n%s", logs)
	}
	for _, s := range []string{"@", "token", "email"} {
		if strings.Contains(strings.ToLower(logs), s) {
			t.Fatalf("log contains %q:\n%s", s, logs)
		}
	}
}
