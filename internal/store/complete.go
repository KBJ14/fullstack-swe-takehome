package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

// DefaultSweepBatch는 식사 종료 순회에서 한 번에 읽는 후보 수다.
const DefaultSweepBatch = 100

// errSeatUnderflow는 좌석을 반환하면 사용 중 좌석이 음수가 되는 데이터 불일치다.
// 임의로 0으로 보정하지 않고 그 팀의 처리를 취소한다.
var errSeatUnderflow = errors.New("returning seats would make seatsInUse negative")

// SweepReport는 식사 종료 순회 한 번의 결과다.
type SweepReport struct {
	// Completed는 이번 순회에서 완료 처리하고 좌석을 반환한 팀 수다.
	Completed int
	// Skipped는 후보였지만 트랜잭션 안에서 다시 확인했을 때 이미 완료됐거나 아직 종료 시각 전이라 건너뛴 팀 수다.
	Skipped int
	// Failures는 처리하지 못한 팀의 오류다. 다른 팀 처리는 계속한다.
	Failures []SweepFailure
}

// SweepFailure는 한 팀의 종료 처리 실패다. 개인정보 없이 문서 ID와 원인만 담는다.
type SweepFailure struct {
	PartyID string
	Err     error
}

// CompleteDue는 종료 예정 시각이 now 이하인 식사 중 팀을 완료 처리하고 좌석을 반환한다.
//
//   - 후보는 (serviceEndsAt, _id) 순으로 batch개씩 읽고, 마지막으로 본 키 다음부터 이어 읽는다.
//     처리에 실패한 팀이 앞에 남아 있어도 뒤의 정상 팀까지 같은 순회에서 처리된다.
//   - 팀마다 별도의 쓰기 트랜잭션(completeOne)으로 처리한다. 한 팀의 실패가 다른 팀의 변경을 취소하지 않는다.
//   - ctx가 끝나면(순회 제한 시간·서버 종료) 남은 후보는 다음 순회로 넘긴다.
//
// now는 호출자가 정한다. (서버 작업은 현재 시각, 테스트는 제어 가능한 시각)
func (s *Store) CompleteDue(ctx context.Context, now time.Time, batch int) (SweepReport, error) {
	if batch <= 0 {
		batch = DefaultSweepBatch
	}
	// BSON 날짜는 밀리초 정밀도다. DB 조건과 Go 쪽 재확인이 같은 기준을 쓰도록 미리 자른다.
	now = now.UTC().Truncate(time.Millisecond)
	var report SweepReport
	var lastEnds time.Time
	var lastID bson.ObjectID
	first := true

	for {
		filter := bson.D{
			{Key: "status", Value: party.StatusDining},
			{Key: "serviceEndsAt", Value: bson.D{{Key: "$lte", Value: now}}},
		}
		if !first {
			// (serviceEndsAt, _id) > (lastEnds, lastID)
			filter = append(filter, bson.E{Key: "$or", Value: bson.A{
				bson.D{{Key: "serviceEndsAt", Value: bson.D{{Key: "$gt", Value: lastEnds}}}},
				bson.D{{Key: "serviceEndsAt", Value: lastEnds}, {Key: "_id", Value: bson.D{{Key: "$gt", Value: lastID}}}},
			}})
		}
		cur, err := s.parties.Find(ctx, filter, options.Find().
			SetSort(bson.D{{Key: "serviceEndsAt", Value: 1}, {Key: "_id", Value: 1}}).
			SetLimit(int64(batch)).
			SetProjection(bson.D{{Key: "_id", Value: 1}, {Key: "serviceEndsAt", Value: 1}}))
		if err != nil {
			return report, fmt.Errorf("find due parties: %w", err)
		}
		var cands []struct {
			ID            bson.ObjectID `bson:"_id"`
			ServiceEndsAt time.Time     `bson:"serviceEndsAt"`
		}
		if err := cur.All(ctx, &cands); err != nil {
			return report, fmt.Errorf("read due parties: %w", err)
		}

		for _, c := range cands {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			done, err := s.completeOne(ctx, c.ID, now)
			switch {
			case err != nil:
				report.Failures = append(report.Failures, SweepFailure{PartyID: c.ID.Hex(), Err: err})
			case done:
				report.Completed++
			default:
				report.Skipped++
			}
			lastEnds, lastID, first = c.ServiceEndsAt, c.ID, false
		}
		if len(cands) < batch {
			return report, nil
		}
	}
}

// completeOne은 한 팀의 완료 처리와 좌석 반환을 하나의 쓰기 트랜잭션으로 처리한다.
//
// 트랜잭션 안에서 다시 확인하는 조건 (충돌로 재시도하면 처음부터 다시 확인):
//   - 팀이 아직 식사 중이고 종료 예정 시각이 now 이하인지. 아니면 아무것도 바꾸지 않는다(false).
//   - 사용 중 좌석이 이 팀 인원 이상인지. 아니면 errSeatUnderflow로 트랜잭션 전체를 취소한다.
//
// 바꾸는 것은 팀의 status(completed)와 식당의 seatsInUse뿐이다.
// 팀 문서는 삭제하지 않고, tokenHash·successorId·시각 필드는 그대로 둔다. 다음 팀을 자동 체크인시키지 않는다.
func (s *Store) completeOne(ctx context.Context, id bson.ObjectID, now time.Time) (bool, error) {
	out, err := s.withTransaction(ctx, func(ctx context.Context) (any, error) {
		p, err := s.findOne(ctx, bson.D{{Key: "_id", Value: id}})
		if err != nil {
			return false, err
		}
		if p == nil || p.Status != party.StatusDining || p.ServiceEndsAt == nil || p.ServiceEndsAt.After(now) {
			// 다른 실행 주체가 먼저 완료했거나, 아직 종료 시각 전이다.
			return false, nil
		}

		res, err := s.parties.UpdateOne(ctx,
			bson.D{
				{Key: "_id", Value: id},
				{Key: "status", Value: party.StatusDining},
				{Key: "serviceEndsAt", Value: bson.D{{Key: "$lte", Value: now}}},
			},
			bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: party.StatusCompleted}}}},
		)
		if err != nil {
			return false, fmt.Errorf("complete party: %w", err)
		}
		if res.MatchedCount != 1 {
			return false, errConcurrentChange
		}

		// 좌석 반환: 사용 중 좌석이 인원 이상일 때만 감소시킨다. 음수가 될 상황이면 보정하지 않고 취소한다.
		res, err = s.restaurants.UpdateOne(ctx,
			bson.D{
				{Key: "_id", Value: RestaurantID},
				{Key: "seatsInUse", Value: bson.D{{Key: "$gte", Value: p.PartySize}}},
			},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "seatsInUse", Value: -p.PartySize}}}},
		)
		if err != nil {
			return false, fmt.Errorf("return seats: %w", err)
		}
		if res.MatchedCount != 1 {
			if err := s.restaurants.FindOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}).Err(); errors.Is(err, mongo.ErrNoDocuments) {
				return false, fmt.Errorf("return seats: restaurant document missing")
			}
			return false, fmt.Errorf("%w: party size %d", errSeatUnderflow, p.PartySize)
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	return out.(bool), nil
}
