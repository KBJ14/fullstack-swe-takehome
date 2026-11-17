package store

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// errBrokenSuccessor는 완료 팀의 successorId가 존재하지 않는 팀을 가리킬 때 반환된다.
// 정상 흐름에서는 생기지 않는 데이터 불일치이므로, 미등록으로 숨기지 않고 내부 오류로 처리한다.
var errBrokenSuccessor = errors.New("successorId points to a missing party")

// resolution은 쿠키 토큰을 해석한 결과다. 등록 API와 조회 API가 같은 규칙을 쓴다.
type resolution struct {
	// cookie는 토큰 해시로 직접 찾은 팀이다. nil이면 미등록 토큰이다.
	cookie *partyDoc
	// target은 쿠키가 현재 가리키는 팀이다.
	//   - cookie가 진행 중이거나, 완료됐지만 successorId가 없으면: cookie 자신
	//   - cookie가 완료됐고 successorId가 있으면: successor (한 단계만 따라감)
	target *partyDoc
}

// viaSuccessor는 target이 successor인지 알려 준다.
func (r resolution) viaSuccessor() bool {
	return r.cookie != nil && r.target != r.cookie
}

// resolveToken은 토큰 해시를 연결 규칙에 따라 해석한다. (docs/design.md 2.8 "쿠키 해석 규칙")
//
// 트랜잭션 안에서 호출해, 해석과 이후의 판단이 같은 스냅샷을 보도록 한다.
func (s *Store) resolveToken(ctx context.Context, tokenHash string) (resolution, error) {
	cur, err := s.findOne(ctx, bson.D{{Key: "tokenHash", Value: tokenHash}})
	if err != nil || cur == nil {
		return resolution{}, err
	}
	if cur.Status.Active() || cur.SuccessorID == nil {
		return resolution{cookie: cur, target: cur}, nil
	}
	succ, err := s.findOne(ctx, bson.D{{Key: "_id", Value: *cur.SuccessorID}})
	if err != nil {
		return resolution{}, err
	}
	if succ == nil {
		return resolution{}, fmt.Errorf("%w: party %s", errBrokenSuccessor, cur.ID.Hex())
	}
	return resolution{cookie: cur, target: succ}, nil
}
