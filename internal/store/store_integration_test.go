//go:build integration

package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/database"
	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

func newTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, err := database.Connect(uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	name := "it_store_" + hex.EncodeToString(buf)
	t.Cleanup(func() {
		if err := client.Database(name).Drop(context.Background()); err != nil {
			t.Errorf("drop temp database: %v", err)
		}
	})
	return ctx, New(client, name)
}

func registerCmd(email string) party.RegisterCommand {
	tok, _ := token.Generate()
	return party.RegisterCommand{
		Input:        party.Input{Name: "P", Email: email, EmailNormalized: party.NormalizeEmail(email), PartySize: 2},
		NewTokenHash: token.Hash(tok),
	}
}

func TestEnsureSchemaIsIdempotentAndKeepsState(t *testing.T) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// 운영 중 바뀐 값을 흉내 낸다.
	if _, err := s.restaurants.UpdateOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "nextSequence", Value: int64(7)}, {Key: "seatsInUse", Value: 3}}}}); err != nil {
		t.Fatal(err)
	}

	// 다시 실행해도 오류가 없고 기존 값을 덮어쓰지 않는다.
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
	var rest struct {
		Capacity     int   `bson:"capacity"`
		SeatsInUse   int   `bson:"seatsInUse"`
		NextSequence int64 `bson:"nextSequence"`
	}
	if err := s.restaurants.FindOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}).Decode(&rest); err != nil {
		t.Fatal(err)
	}
	if rest.Capacity != Capacity || rest.SeatsInUse != 3 || rest.NextSequence != 7 {
		t.Fatalf("restaurant overwritten: %+v", rest)
	}
	if n, _ := s.restaurants.CountDocuments(ctx, bson.D{}); n != 1 {
		t.Fatalf("expected one restaurant document, got %d", n)
	}

	// 인덱스 정의 확인
	cur, err := s.parties.Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var idx []bson.M
	if err := cur.All(ctx, &idx); err != nil {
		t.Fatal(err)
	}
	found := map[string]bson.M{}
	for _, i := range idx {
		found[i["name"].(string)] = i
	}
	for _, name := range []string{indexActiveEmail, indexTokenHash, indexSequence} {
		if i, ok := found[name]; !ok || i["unique"] != true {
			t.Fatalf("index %s missing or not unique: %v", name, i)
		}
	}
	if _, ok := found[indexActiveEmail]["partialFilterExpression"]; !ok {
		t.Fatal("active email index must be partial")
	}
	if i, ok := found[indexWaitingSequence]; !ok || i["partialFilterExpression"] == nil {
		t.Fatalf("waiting sequence index missing or not partial: %v", i)
	}
	if i, ok := found[indexDiningEnds]; !ok || i["partialFilterExpression"] == nil {
		t.Fatalf("dining end index missing or not partial: %v", i)
	}
}

// 앞 팀 수 조회가 대기 중 팀 부분 인덱스를 사용하는지 실행 계획으로 확인한다.
func TestAheadCountQueryUsesWaitingIndex(t *testing.T) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var plan bson.Raw
	err := s.parties.Database().RunCommand(ctx, bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "aggregate", Value: partiesCollection},
			{Key: "pipeline", Value: bson.A{
				bson.D{{Key: "$match", Value: bson.D{
					{Key: "status", Value: party.StatusWaiting},
					{Key: "sequence", Value: bson.D{{Key: "$lt", Value: int64(100)}}},
				}}},
				bson.D{{Key: "$count", Value: "n"}},
			}},
			{Key: "cursor", Value: bson.D{}},
		}},
		{Key: "verbosity", Value: "queryPlanner"},
	}).Decode(&plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), indexWaitingSequence) {
		t.Fatalf("ahead count query does not use %s: %s", indexWaitingSequence, plan.String())
	}
}

func TestStatusReadTimeoutIsBounded(t *testing.T) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s.readTimeout = time.Nanosecond
	start := time.Now()
	if _, err := s.Status(ctx, token.Hash("anything")); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("status read did not respect timeout")
	}
}

func TestRegisterFailsCleanlyWithoutRestaurantDocument(t *testing.T) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.restaurants.DeleteOne(ctx, bson.D{{Key: "_id", Value: RestaurantID}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Register(ctx, registerCmd("a@example.com"))
	if err == nil {
		t.Fatal("expected error when restaurant document is missing")
	}
	if n, _ := s.parties.CountDocuments(ctx, bson.D{}); n != 0 {
		t.Fatal("party inserted without sequence")
	}
}

func TestTransactionTimeoutIsBounded(t *testing.T) {
	ctx, s := newTestStore(t)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s.txTimeout = time.Nanosecond
	start := time.Now()
	if _, err := s.Register(ctx, registerCmd("a@example.com")); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("transaction did not respect timeout")
	}
	if n, _ := s.parties.CountDocuments(ctx, bson.D{}); n != 0 {
		t.Fatal("party inserted after timeout")
	}
}
