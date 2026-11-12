//go:build integration

// 실제 MongoDB replica set에서 트랜잭션 commit과 rollback을 확인하는 통합 테스트.
// MONGODB_URI가 가리키는 서버에 임시 데이터베이스를 만들고, 테스트 후 삭제한다.
package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func setup(t *testing.T) (context.Context, *mongo.Client, *mongo.Collection) {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, err := Connect(uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	if err := (Checker{Client: client}).CheckReady(ctx); err != nil {
		t.Fatalf("mongodb not ready: %v", err)
	}

	// 다른 데이터와 섞이지 않도록 테스트마다 임시 DB 이름을 만든다.
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	db := client.Database("it_tx_" + hex.EncodeToString(buf))
	t.Cleanup(func() {
		if err := db.Drop(context.Background()); err != nil {
			t.Errorf("drop temp database: %v", err)
		}
	})

	coll := db.Collection("items")
	// 트랜잭션 안에서 컬렉션을 암묵적으로 만들지 않도록 미리 만든다.
	if err := db.CreateCollection(ctx, "items"); err != nil {
		t.Fatal(err)
	}
	return ctx, client, coll
}

func count(t *testing.T, ctx context.Context, coll *mongo.Collection) int64 {
	t.Helper()
	n, err := coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTransactionCommit(t *testing.T) {
	ctx, client, coll := setup(t)

	sess, err := client.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.EndSession(ctx)

	_, err = sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if _, err := coll.InsertOne(ctx, bson.D{{Key: "n", Value: 1}}); err != nil {
			return nil, err
		}
		_, err := coll.InsertOne(ctx, bson.D{{Key: "n", Value: 2}})
		return nil, err
	})
	if err != nil {
		t.Fatalf("transaction failed: %v", err)
	}
	if got := count(t, ctx, coll); got != 2 {
		t.Fatalf("expected 2 committed documents, got %d", got)
	}
}

func TestTransactionRollback(t *testing.T) {
	ctx, client, coll := setup(t)

	sess, err := client.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.EndSession(ctx)

	errAbort := errors.New("abort on purpose")
	_, err = sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if _, err := coll.InsertOne(ctx, bson.D{{Key: "n", Value: 1}}); err != nil {
			return nil, err
		}
		// 트랜잭션 안에서는 쓴 문서가 보인다.
		n, err := coll.CountDocuments(ctx, bson.D{})
		if err != nil {
			return nil, err
		}
		if n != 1 {
			t.Errorf("expected 1 document inside transaction, got %d", n)
		}
		return nil, errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatalf("expected abort error, got %v", err)
	}
	if got := count(t, ctx, coll); got != 0 {
		t.Fatalf("expected rollback to leave 0 documents, got %d", got)
	}
}
