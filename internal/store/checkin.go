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

// CheckIn은 토큰 해시가 가리키는 팀을 체크인한다.
//
// 하나의 쓰기 트랜잭션(등록과 같은 5초 제한, snapshot/majority)에서 다음을 처리한다.
// 충돌로 재시도하면 처음부터 다시 읽고 판단한다.
//
//  1. 쿠키 해석 (등록·조회와 같은 resolveToken 규칙)
//     - 미등록 → ErrNotRegistered (401)
//     - 식사 중 → 아무것도 바꾸지 않고 저장된 식사 정보를 돌려준다 (200, already_dining)
//     - 완료 → ErrAlreadyCompleted (409). 식사를 다시 시작하지 않는다
//  2. 선착순 확인: 대기 중 팀 중 순번이 가장 작은 팀이 나여야 한다. 아니면 ErrNotYourTurn (409)
//  3. 팀 문서를 조건부로 갱신한다: status가 waiting일 때만 dining으로 바꾸고 체크인·종료 시각을 저장
//  4. 식당 문서를 조건부로 갱신한다: seatsInUse + 인원 ≤ capacity일 때만 $inc.
//     조건이 맞지 않으면 ErrNotEnoughSeats (409). 트랜잭션이 중단되어 3단계 변경도 취소된다
//
// 동시성
//   - 모든 체크인(그리고 이후의 식사 종료 작업)은 같은 식당 문서를 쓰므로, 동시에 진행된 트랜잭션은
//     쓰기 충돌로 하나만 커밋되고 나머지는 최신 스냅샷에서 1단계부터 다시 판단한다.
//   - 같은 팀의 동시 체크인은 3단계의 같은 팀 문서에서도 충돌한다. 재시도한 쪽은 1단계에서 식사 중을 보고
//     already_dining을 돌려주므로 좌석 차감과 시각 설정은 한 번만 반영된다.
//   - 뒤 팀이 앞 팀의 커밋 전 스냅샷에서 판단하면 2단계에서 ErrNotYourTurn이 된다. 다시 요청하면 최신 상태로 판단한다.
//
// 응답의 체크인·종료 시각은 커밋된 시도에서 저장한 값 그대로다.
func (s *Store) CheckIn(ctx context.Context, tokenHash string) (party.CheckInResult, error) {
	out, err := s.withTransaction(ctx, func(ctx context.Context) (any, error) {
		return s.checkInOnce(ctx, tokenHash)
	})
	if err != nil {
		for _, known := range []error{party.ErrNotRegistered, party.ErrNotYourTurn, party.ErrNotEnoughSeats, party.ErrAlreadyCompleted} {
			if errors.Is(err, known) {
				return party.CheckInResult{}, known
			}
		}
		return party.CheckInResult{}, fmt.Errorf("check in: %w", err)
	}
	return out.(party.CheckInResult), nil
}

// checkInOnce는 트랜잭션 한 번의 시도다. 재시도 시 다시 호출되므로 외부 부작용이 없어야 한다.
func (s *Store) checkInOnce(ctx context.Context, tokenHash string) (party.CheckInResult, error) {
	r, err := s.resolveToken(ctx, tokenHash)
	if err != nil {
		return party.CheckInResult{}, err
	}
	p := r.target
	if p == nil {
		return party.CheckInResult{}, party.ErrNotRegistered
	}

	switch p.Status {
	case party.StatusDining:
		info, err := s.diningInfo(p)
		if err != nil {
			return party.CheckInResult{}, err
		}
		return party.CheckInResult{
			Outcome: party.CheckInAlreadyDining,
			View:    party.StatusView{Party: p.view(), Dining: info},
		}, nil
	case party.StatusCompleted:
		return party.CheckInResult{}, party.ErrAlreadyCompleted
	case party.StatusWaiting:
		// 아래에서 처리
	default:
		return party.CheckInResult{}, fmt.Errorf("%w: party %s has unknown status %q", errIntegrity, p.ID.Hex(), p.Status)
	}

	// 2. 선착순: 대기 중 팀 중 가장 앞선 팀이 나인지 확인한다. (sequence_waiting 부분 인덱스 사용)
	var head struct {
		ID bson.ObjectID `bson:"_id"`
	}
	err = s.parties.FindOne(ctx,
		bson.D{{Key: "status", Value: party.StatusWaiting}},
		options.FindOne().SetSort(bson.D{{Key: "sequence", Value: 1}}).SetProjection(bson.D{{Key: "_id", Value: 1}}),
	).Decode(&head)
	if err != nil {
		// 나 자신이 대기 중이므로 결과가 없을 수 없다.
		return party.CheckInResult{}, fmt.Errorf("find queue head: %w", err)
	}
	if head.ID != p.ID {
		return party.CheckInResult{}, party.ErrNotYourTurn
	}

	// 3. 팀 상태와 시각: 서버 시각 기준. BSON 날짜 정밀도(밀리초)에 맞춰 저장 값과 응답 값을 일치시킨다.
	checkedInAt := s.now().UTC().Truncate(time.Millisecond)
	endsAt := checkedInAt.Add(party.ServiceDuration(p.PartySize))
	res, err := s.parties.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: p.ID}, {Key: "status", Value: party.StatusWaiting}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: party.StatusDining},
			{Key: "checkedInAt", Value: checkedInAt},
			{Key: "serviceEndsAt", Value: endsAt},
		}}},
	)
	if err != nil {
		return party.CheckInResult{}, fmt.Errorf("update party: %w", err)
	}
	if res.MatchedCount != 1 {
		return party.CheckInResult{}, errConcurrentChange
	}

	// 4. 좌석: 정원을 넘지 않을 때만 증가시킨다. 사용 중 좌석은 0~capacity 범위를 벗어나지 않는다.
	res, err = s.restaurants.UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: RestaurantID},
			{Key: "$expr", Value: bson.D{{Key: "$lte", Value: bson.A{
				bson.D{{Key: "$add", Value: bson.A{"$seatsInUse", p.PartySize}}},
				"$capacity",
			}}}},
		},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "seatsInUse", Value: p.PartySize}}}},
	)
	if err != nil {
		return party.CheckInResult{}, fmt.Errorf("reserve seats: %w", err)
	}
	if res.MatchedCount != 1 {
		if err := s.restaurants.FindOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}).Err(); errors.Is(err, mongo.ErrNoDocuments) {
			return party.CheckInResult{}, fmt.Errorf("reserve seats: restaurant document missing")
		}
		return party.CheckInResult{}, party.ErrNotEnoughSeats
	}

	p.Status = party.StatusDining
	p.CheckedInAt = &checkedInAt
	p.ServiceEndsAt = &endsAt
	info, err := s.diningInfo(p)
	if err != nil {
		return party.CheckInResult{}, err
	}
	return party.CheckInResult{
		Outcome: party.CheckInStarted,
		View:    party.StatusView{Party: p.view(), Dining: info},
	}, nil
}
