package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

// errIntegrity는 저장된 문서가 상태 규칙에 맞지 않을 때(예: 식사 중인데 종료 예정 시각이 없음) 반환된다.
var errIntegrity = errors.New("stored party violates state invariants")

// Status는 토큰 해시가 가리키는 팀의 현재 상태를 조회한다.
//
//   - 읽기 전용 스냅샷 트랜잭션 안에서 쿠키 해석, 앞 팀 수, 좌석 수를 모두 같은 시점으로 읽는다.
//   - 아무것도 쓰지 않는다. (팀 상태·좌석·식사 종료를 바꾸지 않음)
//   - 체크인 가능 여부는 이 스냅샷 기준의 안내다. 체크인 API는 순서와 좌석을 다시 확인해야 한다.
func (s *Store) Status(ctx context.Context, tokenHash string) (party.StatusView, error) {
	out, err := s.withReadSnapshot(ctx, func(ctx context.Context) (any, error) {
		return s.statusOnce(ctx, tokenHash)
	})
	if err != nil {
		if errors.Is(err, party.ErrNotRegistered) {
			return party.StatusView{}, party.ErrNotRegistered
		}
		return party.StatusView{}, fmt.Errorf("read party status: %w", err)
	}
	return out.(party.StatusView), nil
}

func (s *Store) statusOnce(ctx context.Context, tokenHash string) (party.StatusView, error) {
	r, err := s.resolveToken(ctx, tokenHash)
	if err != nil {
		return party.StatusView{}, err
	}
	if r.target == nil {
		return party.StatusView{}, party.ErrNotRegistered
	}
	p := r.target
	view := party.StatusView{Party: p.view()}

	switch p.Status {
	case party.StatusWaiting:
		// 내 순번보다 앞선 대기 중 팀만 센다. 식사 중·완료 팀은 제외된다.
		// status 조건이 있어야 sequence_waiting 부분 인덱스를 사용한다.
		ahead, err := s.parties.CountDocuments(ctx, bson.D{
			{Key: "status", Value: party.StatusWaiting},
			{Key: "sequence", Value: bson.D{{Key: "$lt", Value: p.Sequence}}},
		})
		if err != nil {
			return party.StatusView{}, fmt.Errorf("count parties ahead: %w", err)
		}
		var rest struct {
			Capacity   int `bson:"capacity"`
			SeatsInUse int `bson:"seatsInUse"`
		}
		if err := s.restaurants.FindOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}).Decode(&rest); err != nil {
			return party.StatusView{}, fmt.Errorf("read restaurant: %w", err)
		}
		view.Waiting = &party.WaitingInfo{
			AheadCount: ahead,
			CanCheckIn: party.CanCheckIn(ahead, rest.Capacity, rest.SeatsInUse, p.PartySize),
		}

	case party.StatusDining:
		info, err := s.diningInfo(p)
		if err != nil {
			return party.StatusView{}, err
		}
		view.Dining = info

	case party.StatusCompleted:
		// 완료 상태는 Party 정보만 돌려준다.

	default:
		return party.StatusView{}, fmt.Errorf("%w: party %s has unknown status %q", errIntegrity, p.ID.Hex(), p.Status)
	}
	return view, nil
}

// diningInfo는 식사 중인 팀의 저장된 종료 예정 시각과 현재 서버 시각을 돌려준다.
// 조회 API와 체크인 API가 같은 형식을 쓰도록 공유한다.
func (s *Store) diningInfo(p *partyDoc) (*party.DiningInfo, error) {
	if p.ServiceEndsAt == nil {
		return nil, fmt.Errorf("%w: dining party %s has no serviceEndsAt", errIntegrity, p.ID.Hex())
	}
	return &party.DiningInfo{
		ServiceEndsAt: p.ServiceEndsAt.UTC(),
		ServerTime:    s.now().UTC().Truncate(time.Millisecond),
	}, nil
}
