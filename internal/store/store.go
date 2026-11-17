// Package store는 MongoDB에 팀(parties)과 식당(restaurants) 문서를 저장한다.
//
// 문서 구조 (docs/design.md 2.4 참고)
//
//	parties:     팀별 문서. 등록 정보, 등록 순번, 상태, 토큰 검증값 등
//	restaurants: 식당 공통 문서 하나(_id: "default"). 정원, 사용 중 좌석, 등록 순번 카운터
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

const (
	partiesCollection     = "parties"
	restaurantsCollection = "restaurants"

	// RestaurantID는 식당 공통 문서의 _id다. 이 앱은 식당 하나만 관리한다.
	RestaurantID = "default"
	// Capacity는 식당 정원이다. (원문 요구사항: 10석 고정)
	Capacity = 10

	// 인덱스 이름. 중복 키 오류가 어느 인덱스에서 났는지 구분하는 데도 쓴다.
	indexActiveEmail = "emailNormalized_active_unique"
	indexTokenHash   = "tokenHash_unique"
	indexSequence    = "sequence_unique"

	indexWaitingSequence = "sequence_waiting"
	indexDiningEnds      = "serviceEndsAt_dining"

	// DefaultTxTimeout은 쓰기 트랜잭션 한 건(충돌 재시도 포함)에 허용하는 전체 시간이다.
	DefaultTxTimeout = 5 * time.Second
	// DefaultReadTimeout은 상태 조회 스냅샷 한 건에 허용하는 전체 시간이다.
	// 화면이 자주 폴링할 조회이므로 쓰기보다 짧게 두어 DB가 느릴 때 요청이 쌓이지 않게 한다.
	DefaultReadTimeout = 2 * time.Second
)

// partyDoc은 parties 컬렉션의 문서 구조다.
type partyDoc struct {
	ID              bson.ObjectID `bson:"_id"`
	Sequence        int64         `bson:"sequence"`
	Name            string        `bson:"name"`
	Email           string        `bson:"email"`
	EmailNormalized string        `bson:"emailNormalized"`
	PartySize       int           `bson:"partySize"`
	Status          party.Status  `bson:"status"`
	// TokenHash는 식별 토큰의 SHA-256 값이다. 토큰 원문은 저장하지 않는다.
	TokenHash string    `bson:"tokenHash"`
	CreatedAt time.Time `bson:"createdAt"`
	// CheckedInAt, ServiceEndsAt은 체크인 기능에서 채운다. (아직 미구현)
	CheckedInAt   *time.Time `bson:"checkedInAt,omitempty"`
	ServiceEndsAt *time.Time `bson:"serviceEndsAt,omitempty"`
	// SuccessorID는 이 완료 팀의 쿠키로 재등록해 만든 새 팀의 _id다.
	// 같은 완료 쿠키로 동시에 재등록해도 새 팀이 하나만 생기도록 하는 데 쓴다.
	SuccessorID *bson.ObjectID `bson:"successorId,omitempty"`
}

func (d partyDoc) view() party.View {
	return party.View{
		Name:         d.Name,
		PartySize:    d.PartySize,
		Status:       d.Status,
		RegisteredAt: d.CreatedAt.UTC(),
	}
}

// Store는 MongoDB 저장소다.
type Store struct {
	client      *mongo.Client
	parties     *mongo.Collection
	restaurants *mongo.Collection
	txTimeout   time.Duration
	readTimeout time.Duration
	now         func() time.Time
}

// New는 db 데이터베이스를 사용하는 Store를 만든다.
func New(client *mongo.Client, db string) *Store {
	d := client.Database(db)
	return &Store{
		client:      client,
		parties:     d.Collection(partiesCollection),
		restaurants: d.Collection(restaurantsCollection),
		txTimeout:   DefaultTxTimeout,
		readTimeout: DefaultReadTimeout,
		now:         time.Now,
	}
}

// EnsureSchema는 인덱스와 초기 식당 문서를 준비한다. 여러 번 실행해도 안전하다.
//
//   - 같은 이름·같은 정의의 인덱스가 이미 있으면 MongoDB는 아무것도 바꾸지 않는다.
//   - 식당 문서는 없을 때만 $setOnInsert로 만든다. 기존 값(사용 중 좌석, 카운터)은 덮어쓰지 않는다.
func (s *Store) EnsureSchema(ctx context.Context) error {
	active := bson.A{}
	for _, st := range party.ActiveStatuses {
		active = append(active, st)
	}
	_, err := s.parties.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			// 진행 중(대기·식사) 팀 사이에서만 정규화 이메일이 고유하다.
			// 완료 팀은 인덱스에 포함되지 않으므로 같은 이메일로 재등록할 수 있다.
			// MongoDB 8.0은 partialFilterExpression에서 $in을 지원한다.
			Keys: bson.D{{Key: "emailNormalized", Value: 1}},
			Options: options.Index().
				SetName(indexActiveEmail).
				SetUnique(true).
				SetPartialFilterExpression(bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: active}}}}),
		},
		{
			Keys:    bson.D{{Key: "tokenHash", Value: 1}},
			Options: options.Index().SetName(indexTokenHash).SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "sequence", Value: 1}},
			Options: options.Index().SetName(indexSequence).SetUnique(true),
		},
		{
			// 대기 중 팀만 담는 순번 인덱스. "내 앞의 대기 팀 수"를 셀 때
			// 완료·식사 중 팀(시간이 지날수록 늘어남)을 훑지 않고 대기 팀만 범위 조회한다.
			// 체크인 시 맨 앞 대기 팀을 찾을 때도 같은 인덱스를 쓸 수 있다.
			Keys: bson.D{{Key: "sequence", Value: 1}},
			Options: options.Index().
				SetName(indexWaitingSequence).
				SetPartialFilterExpression(bson.D{{Key: "status", Value: party.StatusWaiting}}),
		},
		{
			// 식사 중 팀만 담는 종료 시각 인덱스. 식사 종료 작업이 "종료 시각이 지난 식사 중 팀"을
			// 종료 시각·_id 순으로 조금씩 읽는 데 쓴다. 완료 팀(계속 늘어남)은 포함하지 않는다.
			Keys: bson.D{{Key: "serviceEndsAt", Value: 1}, {Key: "_id", Value: 1}},
			Options: options.Index().
				SetName(indexDiningEnds).
				SetPartialFilterExpression(bson.D{{Key: "status", Value: party.StatusDining}}),
		},
	})
	if err != nil {
		return fmt.Errorf("create party indexes: %w", err)
	}

	_, err = s.restaurants.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: RestaurantID}},
		bson.D{{Key: "$setOnInsert", Value: bson.D{
			{Key: "capacity", Value: Capacity},
			{Key: "seatsInUse", Value: 0},
			{Key: "nextSequence", Value: int64(0)},
		}}},
		options.UpdateOne().SetUpsert(true),
	)
	// 여러 인스턴스가 동시에 upsert하면 한쪽이 중복 키 오류를 받을 수 있다. 문서가 이미 생긴 것이므로 성공으로 본다.
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("ensure restaurant document: %w", err)
	}
	return nil
}

// withTransaction은 fn을 쓰기 트랜잭션으로 실행한다.
// (read concern snapshot, write concern majority, 제한 시간 txTimeout)
func (s *Store) withTransaction(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	txOpts := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())
	return s.runTransaction(ctx, s.txTimeout, txOpts, fn)
}

// withReadSnapshot은 fn을 읽기 전용 트랜잭션으로 실행한다.
//
// read concern snapshot: fn 안의 모든 읽기가 같은 시점의 데이터를 본다.
// 예를 들어 팀 상태, 앞 팀 수, 좌석 수가 서로 다른 시점에서 읽혀 모순되는 일이 없다.
// fn은 쓰기를 하지 않아야 한다.
func (s *Store) withReadSnapshot(ctx context.Context, fn func(ctx context.Context) (any, error)) (any, error) {
	txOpts := options.Transaction().SetReadConcern(readconcern.Snapshot())
	return s.runTransaction(ctx, s.readTimeout, txOpts, fn)
}

// runTransaction은 fn을 하나의 트랜잭션으로 실행한다.
//
//   - 충돌 등 일시적 오류(TransientTransactionError)는 드라이버가 fn 전체를 다시 실행한다.
//     fn은 매 시도마다 필요한 상태와 조건을 DB에서 다시 읽어야 하며,
//     HTTP 응답·쿠키 발급 같은 외부 부작용을 일으키면 안 된다.
//   - 재시도를 포함한 전체 시간은 timeout으로 제한한다.
func (s *Store) runTransaction(ctx context.Context, timeout time.Duration, txOpts *options.TransactionOptionsBuilder,
	fn func(ctx context.Context) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sess, err := s.client.StartSession()
	if err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))

	return sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		// 제한 시간이 지났으면 더 시도하지 않는다.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return fn(ctx)
	}, txOpts)
}

// isDuplicateOn은 err가 지정한 인덱스의 중복 키 오류인지 확인한다.
func isDuplicateOn(err error, index string) bool {
	var we mongo.WriteException
	if !errors.As(err, &we) {
		return false
	}
	for _, e := range we.WriteErrors {
		// MongoDB 중복 키 오류 메시지는 "... index: <이름> dup key: ..." 형식이다.
		if e.Code == 11000 && strings.Contains(e.Message, "index: "+index+" ") {
			return true
		}
	}
	return false
}
