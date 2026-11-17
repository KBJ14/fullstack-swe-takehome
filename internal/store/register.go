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

// errConcurrentChange는 같은 트랜잭션 스냅샷에서 확인한 조건이 갱신 시점에 맞지 않을 때 쓴다.
// 정상적으로는 쓰기 충돌(재시도 대상)로 먼저 감지되므로 도달하지 않아야 한다.
var errConcurrentChange = errors.New("registration state changed during transaction")

// Register는 등록 규칙을 하나의 트랜잭션으로 적용한다.
//
// 트랜잭션 안의 처리 순서 (충돌로 재시도하면 처음부터 다시 확인한다)
//
//  1. 요청 쿠키의 토큰 해시로 팀을 찾는다.
//     - 진행 중(대기·식사)이면 아무것도 쓰지 않고 그 팀을 돌려준다. (OutcomeExisting)
//     - 완료 팀이고 이미 이 쿠키로 재등록한 새 팀(SuccessorID)이 진행 중이면 그 팀을 돌려준다.
//     - 완료 팀이고 아직 재등록하지 않았으면, 이번 등록의 "선행 팀"으로 표시한다.
//     - 찾지 못하면 쿠키가 없는 것과 같이 처리한다. (잘못된 쿠키는 어떤 권한도 주지 않음)
//  2. 선행 팀이 있으면 그 문서에 SuccessorID를 조건부로 기록한다.
//     같은 완료 쿠키로 동시에 들어온 다른 트랜잭션은 이 문서에서 쓰기 충돌이 나서 재시도하고,
//     재시도에서는 1단계에서 이미 만들어진 새 팀을 보게 된다.
//  3. 식당 문서의 nextSequence를 $inc로 원자적으로 증가시켜 등록 순번을 받는다.
//  4. 새 팀 문서를 대기 중 상태로 삽입한다.
//     진행 중인 같은 정규화 이메일이 있으면 부분 고유 인덱스가 거부하고 ErrEmailInUse를 돌려준다.
//     이때 트랜잭션이 중단되어 2·3단계의 변경도 함께 취소된다.
//
// 등록은 좌석(seatsInUse)을 바꾸지 않는다.
func (s *Store) Register(ctx context.Context, cmd party.RegisterCommand) (party.RegisterResult, error) {
	out, err := s.withTransaction(ctx, func(ctx context.Context) (any, error) {
		return s.registerOnce(ctx, cmd)
	})
	if err != nil {
		if errors.Is(err, party.ErrEmailInUse) {
			return party.RegisterResult{}, party.ErrEmailInUse
		}
		return party.RegisterResult{}, fmt.Errorf("register party: %w", err)
	}
	return out.(party.RegisterResult), nil
}

// registerOnce는 트랜잭션 한 번의 시도다. 재시도 시 다시 호출되므로 외부 부작용이 없어야 한다.
func (s *Store) registerOnce(ctx context.Context, cmd party.RegisterCommand) (party.RegisterResult, error) {
	var predecessor *partyDoc

	if cmd.PresentedTokenHash != "" {
		cur, err := s.findOne(ctx, bson.D{{Key: "tokenHash", Value: cmd.PresentedTokenHash}})
		if err != nil {
			return party.RegisterResult{}, err
		}
		switch {
		case cur == nil:
			// 잘못된 쿠키: 쿠키가 없는 요청과 같이 처리한다.
		case cur.Status.Active():
			return existing(cur), nil
		case cur.SuccessorID != nil:
			succ, err := s.findOne(ctx, bson.D{{Key: "_id", Value: *cur.SuccessorID}})
			if err != nil {
				return party.RegisterResult{}, err
			}
			if succ != nil && succ.Status.Active() {
				return existing(succ), nil
			}
			// 재등록한 팀도 이미 완료됐다. 이 오래된 쿠키는 더 이상 권한을 주지 않으므로
			// 쿠키가 없는 요청과 같이 처리한다. (docs/design.md 한계 참고)
		default:
			predecessor = cur
		}
	}

	newID := bson.NewObjectID()

	if predecessor != nil {
		res, err := s.parties.UpdateOne(ctx,
			bson.D{
				{Key: "_id", Value: predecessor.ID},
				{Key: "status", Value: party.StatusCompleted},
				{Key: "successorId", Value: bson.D{{Key: "$exists", Value: false}}},
			},
			bson.D{{Key: "$set", Value: bson.D{{Key: "successorId", Value: newID}}}},
		)
		if err != nil {
			return party.RegisterResult{}, err
		}
		if res.MatchedCount != 1 {
			return party.RegisterResult{}, errConcurrentChange
		}
	}

	var counter struct {
		NextSequence int64 `bson:"nextSequence"`
	}
	err := s.restaurants.FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: RestaurantID}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "nextSequence", Value: int64(1)}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return party.RegisterResult{}, fmt.Errorf("issue sequence: %w", err)
	}

	doc := partyDoc{
		ID:              newID,
		Sequence:        counter.NextSequence,
		Name:            cmd.Input.Name,
		Email:           cmd.Input.Email,
		EmailNormalized: cmd.Input.EmailNormalized,
		PartySize:       cmd.Input.PartySize,
		Status:          party.StatusWaiting,
		TokenHash:       cmd.NewTokenHash,
		// BSON 날짜는 밀리초 정밀도이므로 저장 값과 응답 값을 맞추기 위해 미리 자른다.
		CreatedAt: s.now().UTC().Truncate(time.Millisecond),
	}
	if _, err := s.parties.InsertOne(ctx, doc); err != nil {
		if isDuplicateOn(err, indexActiveEmail) {
			return party.RegisterResult{}, party.ErrEmailInUse
		}
		return party.RegisterResult{}, fmt.Errorf("insert party: %w", err)
	}

	return party.RegisterResult{Outcome: party.OutcomeCreated, Party: doc.view()}, nil
}

func existing(d *partyDoc) party.RegisterResult {
	return party.RegisterResult{Outcome: party.OutcomeExisting, Party: d.view()}
}

// findOne은 조건에 맞는 팀 문서를 찾는다. 없으면 nil을 돌려준다.
func (s *Store) findOne(ctx context.Context, filter bson.D) (*partyDoc, error) {
	var d partyDoc
	err := s.parties.FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find party: %w", err)
	}
	return &d, nil
}
