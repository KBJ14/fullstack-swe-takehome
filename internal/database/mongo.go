// Package database는 MongoDB 연결과 준비 상태 확인을 담당한다.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Connect는 MongoDB 클라이언트를 만든다.
//
// 드라이버는 연결을 지연해서 맺으므로, 이 함수가 성공해도 DB가 준비됐다는 뜻은 아니다.
// 준비 여부는 CheckReady로 확인한다.
func Connect(uri string) (*mongo.Client, error) {
	opts := options.Client().
		ApplyURI(uri).
		// 서버를 찾지 못할 때 요청이 너무 오래 매달리지 않도록 상한을 둔다.
		SetServerSelectionTimeout(5 * time.Second)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("create mongo client: %w", err)
	}
	return client, nil
}

// helloResult는 hello 명령 응답 중 준비 상태 판단에 필요한 필드다.
type helloResult struct {
	SetName           string `bson:"setName"`
	IsWritablePrimary bool   `bson:"isWritablePrimary"`
}

// ErrNotReplicaSet은 연결된 서버가 replica set 멤버가 아닐 때 반환된다.
// 트랜잭션은 replica set(또는 sharded cluster)에서만 동작한다.
var ErrNotReplicaSet = errors.New("mongodb is not a replica set member")

// ErrNoWritablePrimary는 replica set에 쓰기 가능한 primary가 없을 때 반환된다.
var ErrNoWritablePrimary = errors.New("mongodb replica set has no writable primary")

// Checker는 MongoDB의 준비 상태를 확인한다.
type Checker struct {
	Client *mongo.Client
}

// CheckReady는 DB에 연결할 수 있고, replica set의 쓰기 가능한 primary에
// 접속됐는지 확인한다. 제한 시간은 호출자가 ctx로 정한다.
func (c Checker) CheckReady(ctx context.Context) error {
	var res helloResult
	err := c.Client.Database("admin").
		RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).
		Decode(&res)
	if err != nil {
		return fmt.Errorf("run hello: %w", err)
	}
	if res.SetName == "" {
		return ErrNotReplicaSet
	}
	if !res.IsWritablePrimary {
		return ErrNoWritablePrimary
	}
	return nil
}
