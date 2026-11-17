//go:build integration

// 식사 종료 처리(store.CompleteDue) 이후 API 동작과, 체크인·종료가 동시에 실행될 때의 좌석 불변 조건을 검증한다.
// 시간 경계는 실제로 기다리지 않고 CompleteDue에 넘기는 시각으로 제어한다.
package api

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

func (e *env) completeDue(now time.Time) int {
	e.t.Helper()
	rep, err := e.st.CompleteDue(e.ctx(), now, 0)
	if err != nil || len(rep.Failures) != 0 {
		e.t.Fatalf("CompleteDue: %+v %v", rep, err)
	}
	return rep.Completed
}

func TestAfterServiceEndStatusSeatsAndReRegistration(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 6), "")
	b := e.post(reqBody("B", "b@example.com", 5), "")

	if r := e.checkIn(a.cookie, nil); r.status != http.StatusOK {
		t.Fatalf("check-in A: %d %s", r.status, r.body)
	}
	endsAt := docTime(t, e.partyByCookie(a.cookie)["serviceEndsAt"])

	// 좌석 부족(빈 좌석 4 < 5)으로 B는 체크인 불가
	if bv := e.getMe(b.cookie).view; bv.Waiting.AheadCount != 0 || bv.Waiting.CanCheckIn {
		t.Fatalf("B before end: %+v", bv.Waiting)
	}

	// 종료 시각 전: 변화 없음
	if n := e.completeDue(endsAt.Add(-time.Millisecond)); n != 0 || seatsInUse(e) != 6 {
		t.Fatalf("before end: completed %d seats %d", n, seatsInUse(e))
	}

	// 종료 시각 도달: A 완료, 좌석 반환, B는 자동 체크인되지 않고 체크인 가능으로 바뀜
	if n := e.completeDue(endsAt); n != 1 {
		t.Fatalf("completed %d", n)
	}
	if seatsInUse(e) != 0 {
		t.Fatalf("seatsInUse %d", seatsInUse(e))
	}
	if r := e.getMe(a.cookie); r.status != http.StatusOK || r.view.Party.Status != party.StatusCompleted || r.view.Dining != nil {
		t.Fatalf("A after end: %d %s", r.status, r.body)
	}
	bv := e.getMe(b.cookie).view
	if bv.Party.Status != party.StatusWaiting || !bv.Waiting.CanCheckIn {
		t.Fatalf("B after end: %+v", bv)
	}

	// 완료 팀의 체크인 재요청은 409, 같은 이메일로 재등록은 가능
	if r := e.checkIn(a.cookie, nil); r.status != http.StatusConflict || r.errCode != "already_completed" {
		t.Fatalf("re-check-in A: %d %s", r.status, r.body)
	}
	if r := e.post(reqBody("A", "A@example.com", 2), ""); r.status != http.StatusCreated {
		t.Fatalf("re-register same email: %d %s", r.status, r.body)
	}
}

// 체크인 요청과 식사 종료 순회를 동시에 반복한다.
// 종료 순회는 먼 미래 시각을 넘겨, 식사 중인 팀을 모두 종료 대상으로 본다(실제 대기 없음).
// 진행 중과 종료 후 모두: 좌석은 0~10, 종료 후에는 좌석 = 실제 식사 중 인원 합.
func TestConcurrentCheckInAndServiceEndKeepSeatsConsistent(t *testing.T) {
	e := newEnv(t)
	sizes := []int{4, 3, 5, 2, 6, 1, 3, 2, 4, 1}
	cookies := make([]string, len(sizes))
	for i, s := range sizes {
		cookies[i] = e.post(reqBody(fmt.Sprintf("P%d", i), fmt.Sprintf("p%d@example.com", i), s), "").cookie
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	var outOfRange atomic.Int32

	// 체크인: 모든 팀이 계속 시도 (대기 중이 아니면 409·200이 돌아오며 아무것도 바뀌지 않음)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for _, c := range cookies {
					r := e.checkIn(c, nil)
					if r.status != http.StatusOK && r.status != http.StatusConflict {
						t.Errorf("check-in: %d %s", r.status, r.body)
						return
					}
				}
			}
		}()
	}
	// 종료 순회: 여러 실행 주체가 동시에
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				rep, err := e.st.CompleteDue(e.ctx(), time.Now().Add(time.Hour), 0)
				if err != nil || len(rep.Failures) != 0 {
					t.Errorf("CompleteDue: %+v %v", rep, err)
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	// 관찰: 좌석이 범위를 벗어나는 순간이 있는지
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if s := seatsInUse(e); s < 0 || s > 10 {
				outOfRange.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// 모든 팀이 완료될 때까지(또는 시간 상한) 진행
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if e.count(bson.D{{Key: "status", Value: "completed"}}) == int64(len(sizes)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()

	if outOfRange.Load() > 0 {
		t.Fatalf("seatsInUse left 0..10 range %d times", outOfRange.Load())
	}
	// 정지 후: 좌석 = 식사 중 인원 합
	cur, err := e.db.Collection("parties").Find(e.ctx(), bson.D{{Key: "status", Value: "dining"}})
	if err != nil {
		t.Fatal(err)
	}
	var dining []struct {
		PartySize int `bson:"partySize"`
	}
	if err := cur.All(e.ctx(), &dining); err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, d := range dining {
		sum += d.PartySize
	}
	if got := seatsInUse(e); got != int64(sum) {
		t.Fatalf("seatsInUse %d, dining sum %d", got, sum)
	}
	// 등록 순서대로 모두 입장·완료했는지 (자동 체크인이 아니라 요청에 의한 체크인)
	if n := e.count(bson.D{{Key: "status", Value: "completed"}}); n != int64(len(sizes)) {
		t.Fatalf("completed %d of %d", n, len(sizes))
	}
	if seatsInUse(e) != 0 {
		t.Fatalf("final seatsInUse %d", seatsInUse(e))
	}
}
