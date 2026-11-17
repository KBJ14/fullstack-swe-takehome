//go:build integration

package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

// base는 테스트용 고정 시각이다. 시간 경계는 실제로 기다리지 않고 CompleteDue의 now로 제어한다.
var base = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

var seqCounter int64

// insertParty는 체크인 기능을 거치지 않고 팀 문서를 직접 만드는 fixture다.
func insertParty(t *testing.T, ctx context.Context, s *Store, size int, st party.Status, endsAt *time.Time) *partyDoc {
	t.Helper()
	seqCounter++
	tok, _ := token.Generate()
	d := partyDoc{
		ID: bson.NewObjectID(), Sequence: seqCounter, Name: "P", Email: "p@example.com",
		EmailNormalized: "p" + bson.NewObjectID().Hex() + "@example.com",
		PartySize:       size, Status: st, TokenHash: token.Hash(tok), CreatedAt: base,
	}
	if endsAt != nil {
		checked := endsAt.Add(-party.ServiceDuration(size))
		d.CheckedInAt, d.ServiceEndsAt = &checked, endsAt
	}
	if _, err := s.parties.InsertOne(ctx, d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func setSeats(t *testing.T, ctx context.Context, s *Store, n int) {
	t.Helper()
	if _, err := s.restaurants.UpdateOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "seatsInUse", Value: n}}}}); err != nil {
		t.Fatal(err)
	}
}

func seats(t *testing.T, ctx context.Context, s *Store) int {
	t.Helper()
	var r struct {
		SeatsInUse int `bson:"seatsInUse"`
	}
	if err := s.restaurants.FindOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r.SeatsInUse
}

func load(t *testing.T, ctx context.Context, s *Store, id bson.ObjectID) bson.Raw {
	t.Helper()
	raw, err := s.parties.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func statusOf(t *testing.T, ctx context.Context, s *Store, id bson.ObjectID) party.Status {
	return party.Status(load(t, ctx, s, id).Lookup("status").StringValue())
}

func ready(t *testing.T) (context.Context, *Store) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, s
}

func ptr(t time.Time) *time.Time { return &t }

func TestCompleteDueRespectsEndTimeBoundary(t *testing.T) {
	ctx, s := ready(t)
	ends := base.Add(10 * time.Second)
	p := insertParty(t, ctx, s, 3, party.StatusDining, &ends)
	setSeats(t, ctx, s, 3)

	// 종료 1ms 전: 아무것도 바뀌지 않는다.
	rep, err := s.CompleteDue(ctx, ends.Add(-time.Millisecond), 0)
	if err != nil || rep.Completed != 0 || statusOf(t, ctx, s, p.ID) != party.StatusDining || seats(t, ctx, s) != 3 {
		t.Fatalf("before end: %+v %v", rep, err)
	}
	// 종료 시각과 같으면 완료되고 정확히 인원만큼 반환된다.
	rep, err = s.CompleteDue(ctx, ends, 0)
	if err != nil || rep.Completed != 1 || statusOf(t, ctx, s, p.ID) != party.StatusCompleted || seats(t, ctx, s) != 0 {
		t.Fatalf("at end: %+v %v", rep, err)
	}
}

func TestCompleteDueOnlyTouchesDueDiningParties(t *testing.T) {
	ctx, s := ready(t)
	due := insertParty(t, ctx, s, 3, party.StatusDining, ptr(base.Add(-time.Second)))
	notYet := insertParty(t, ctx, s, 4, party.StatusDining, ptr(base.Add(time.Second)))
	waiting := insertParty(t, ctx, s, 2, party.StatusWaiting, nil)
	succ := bson.NewObjectID()
	if _, err := s.parties.UpdateOne(ctx, bson.D{{Key: "_id", Value: due.ID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "successorId", Value: succ}}}}); err != nil {
		t.Fatal(err)
	}
	setSeats(t, ctx, s, 7)
	dueBefore := load(t, ctx, s, due.ID)
	notYetBefore, waitingBefore := load(t, ctx, s, notYet.ID), load(t, ctx, s, waiting.ID)

	rep, err := s.CompleteDue(ctx, base, 0)
	if err != nil || rep.Completed != 1 || len(rep.Failures) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if got := seats(t, ctx, s); got != 4 {
		t.Fatalf("seatsInUse %d", got)
	}
	if !bytes.Equal(notYetBefore, load(t, ctx, s, notYet.ID)) || !bytes.Equal(waitingBefore, load(t, ctx, s, waiting.ID)) {
		t.Fatal("non-due parties changed")
	}
	// 완료 팀은 삭제되지 않고 status만 바뀐다. (tokenHash, successorId, 시각 그대로)
	after := load(t, ctx, s, due.ID)
	for _, k := range []string{"tokenHash", "successorId", "checkedInAt", "serviceEndsAt", "sequence", "emailNormalized"} {
		if !after.Lookup(k).Equal(dueBefore.Lookup(k)) {
			t.Fatalf("field %s changed", k)
		}
	}
	if after.Lookup("status").StringValue() != "completed" {
		t.Fatal("not completed")
	}
	// 다음 팀을 자동 체크인시키지 않는다.
	if statusOf(t, ctx, s, waiting.ID) != party.StatusWaiting {
		t.Fatal("waiting party was auto checked in")
	}
}

func TestCompleteDueRepeatedAndConcurrentReturnsSeatsOnce(t *testing.T) {
	ctx, s := ready(t)
	a := insertParty(t, ctx, s, 5, party.StatusDining, ptr(base.Add(-time.Second)))
	b := insertParty(t, ctx, s, 3, party.StatusDining, ptr(base.Add(-time.Second)))
	setSeats(t, ctx, s, 9) // 5 + 3 + 다른 식사 중 1명분이라고 가정
	insertParty(t, ctx, s, 1, party.StatusDining, ptr(base.Add(time.Hour)))

	const n = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep, err := s.CompleteDue(ctx, base, 0)
			if err != nil || len(rep.Failures) != 0 {
				t.Errorf("%+v %v", rep, err)
			}
			mu.Lock()
			total += rep.Completed
			mu.Unlock()
		}()
	}
	wg.Wait()
	for i := 0; i < 3; i++ { // 이후 순차 반복
		rep, _ := s.CompleteDue(ctx, base, 0)
		total += rep.Completed
	}
	if total != 2 {
		t.Fatalf("completed %d times, want 2", total)
	}
	if got := seats(t, ctx, s); got != 1 {
		t.Fatalf("seatsInUse %d, want 1", got)
	}
	if statusOf(t, ctx, s, a.ID) != party.StatusCompleted || statusOf(t, ctx, s, b.ID) != party.StatusCompleted {
		t.Fatal("due parties not completed")
	}
}

func TestCompleteDueUnderflowIsRolledBackAndDoesNotBlockOthers(t *testing.T) {
	ctx, s := ready(t)
	// 데이터 불일치: 앞선 팀(4명)을 반환하면 좌석이 음수가 된다.
	broken := insertParty(t, ctx, s, 4, party.StatusDining, ptr(base.Add(-2*time.Second)))
	ok := insertParty(t, ctx, s, 2, party.StatusDining, ptr(base.Add(-time.Second)))
	setSeats(t, ctx, s, 2)
	brokenBefore := load(t, ctx, s, broken.ID)

	// batch=1: 실패한 팀이 맨 앞에 있어도 같은 순회에서 다음 팀까지 처리해야 한다.
	rep, err := s.CompleteDue(ctx, base, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Completed != 1 || len(rep.Failures) != 1 || rep.Failures[0].PartyID != broken.ID.Hex() ||
		!errors.Is(rep.Failures[0].Err, errSeatUnderflow) {
		t.Fatalf("%+v", rep)
	}
	// 실패한 팀: 상태 변경이 취소되어 그대로 식사 중. 좌석은 0으로 보정되지 않는다.
	if !bytes.Equal(brokenBefore, load(t, ctx, s, broken.ID)) {
		t.Fatal("partial change left on failed party")
	}
	if statusOf(t, ctx, s, ok.ID) != party.StatusCompleted || seats(t, ctx, s) != 0 {
		t.Fatalf("ok party: %s seats %d", statusOf(t, ctx, s, ok.ID), seats(t, ctx, s))
	}
	// 다음 순회에서도 같은 실패를 보고하고 좌석을 건드리지 않는다.
	rep, _ = s.CompleteDue(ctx, base, 1)
	if rep.Completed != 0 || len(rep.Failures) != 1 || seats(t, ctx, s) != 0 {
		t.Fatalf("second sweep: %+v seats %d", rep, seats(t, ctx, s))
	}
}

func TestCompleteDueRecoversAfterError(t *testing.T) {
	ctx, s := ready(t)
	p := insertParty(t, ctx, s, 3, party.StatusDining, ptr(base.Add(-time.Second)))
	// 식당 문서가 없는 상태: 처리 실패, 팀 변경 없음
	if _, err := s.restaurants.DeleteOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}); err != nil {
		t.Fatal(err)
	}
	rep, _ := s.CompleteDue(ctx, base, 0)
	if rep.Completed != 0 || len(rep.Failures) != 1 || statusOf(t, ctx, s, p.ID) != party.StatusDining {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(rep.Failures[0].Err.Error(), "restaurant document missing") {
		t.Fatalf("unexpected error %v", rep.Failures[0].Err)
	}
	// 원인이 해소되면 다음 순회에서 처리된다.
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	setSeats(t, ctx, s, 3)
	rep, err := s.CompleteDue(ctx, base, 0)
	if err != nil || rep.Completed != 1 || seats(t, ctx, s) != 0 {
		t.Fatalf("after recovery: %+v %v seats %d", rep, err, seats(t, ctx, s))
	}
}

func TestCompleteDuePaginatesAllDueParties(t *testing.T) {
	ctx, s := ready(t)
	// 같은 종료 시각을 포함해 7팀. batch=2로 여러 번 나눠 읽어도 모두 처리된다.
	for i := 0; i < 7; i++ {
		insertParty(t, ctx, s, 1, party.StatusDining, ptr(base.Add(-time.Duration(i%3)*time.Second)))
	}
	setSeats(t, ctx, s, 7)
	rep, err := s.CompleteDue(ctx, base, 2)
	if err != nil || rep.Completed != 7 || seats(t, ctx, s) != 0 {
		t.Fatalf("%+v %v seats %d", rep, err, seats(t, ctx, s))
	}
}

func TestCompleteDueStopsOnCancelledContext(t *testing.T) {
	ctx, s := ready(t)
	insertParty(t, ctx, s, 1, party.StatusDining, ptr(base.Add(-time.Second)))
	setSeats(t, ctx, s, 1)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.CompleteDue(cctx, base, 0); err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if seats(t, ctx, s) != 1 {
		t.Fatal("cancelled sweep changed data")
	}
}

func TestDueQueryUsesDiningEndsIndex(t *testing.T) {
	ctx, s := ready(t)
	var plan bson.Raw
	err := s.parties.Database().RunCommand(ctx, bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "find", Value: partiesCollection},
			{Key: "filter", Value: bson.D{
				{Key: "status", Value: party.StatusDining},
				{Key: "serviceEndsAt", Value: bson.D{{Key: "$lte", Value: base}}},
			}},
			{Key: "sort", Value: bson.D{{Key: "serviceEndsAt", Value: 1}, {Key: "_id", Value: 1}}},
			{Key: "limit", Value: 100},
		}},
		{Key: "verbosity", Value: "queryPlanner"},
	}).Decode(&plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), indexDiningEnds) {
		t.Fatalf("due query does not use %s: %s", indexDiningEnds, plan.String())
	}
}
