//go:build integration

// 실제 MongoDB와 실제 순회 주기로 식사 종료 작업을 검증한다. HTTP 요청은 보내지 않는다.
package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/KBJ14/fullstack-swe-takehome/internal/database"
	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

type fixture struct {
	ctx context.Context
	st  *store.Store
	db  *mongo.Database
}

func newFixture(t *testing.T) fixture {
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
	name := "it_worker_" + hex.EncodeToString(buf)
	db := client.Database(name)
	t.Cleanup(func() {
		if err := db.Drop(context.Background()); err != nil {
			t.Errorf("drop temp database: %v", err)
		}
	})
	st := store.New(client, name)
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture{ctx: ctx, st: st, db: db}
}

// dining은 등록 후 체크인 기능 없이 식사 중 상태와 좌석을 만드는 fixture다. 토큰 해시를 돌려준다.
func (f fixture) dining(t *testing.T, email string, size int, endsAt time.Time) string {
	t.Helper()
	tok, _ := token.Generate()
	hash := token.Hash(tok)
	if _, err := f.st.Register(f.ctx, party.RegisterCommand{
		Input:        party.Input{Name: "P", Email: email, EmailNormalized: email, PartySize: size},
		NewTokenHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Collection("parties").UpdateOne(f.ctx, bson.D{{Key: "tokenHash", Value: hash}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: party.StatusDining},
			{Key: "checkedInAt", Value: endsAt.Add(-party.ServiceDuration(size))},
			{Key: "serviceEndsAt", Value: endsAt},
		}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Collection("restaurants").UpdateOne(f.ctx, bson.D{{Key: "_id", Value: store.RestaurantID}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "seatsInUse", Value: size}}}}); err != nil {
		t.Fatal(err)
	}
	return hash
}

func (f fixture) status(t *testing.T, hash string) string {
	t.Helper()
	raw, err := f.db.Collection("parties").FindOne(f.ctx, bson.D{{Key: "tokenHash", Value: hash}}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw.Lookup("status").StringValue()
}

func (f fixture) seats(t *testing.T) int32 {
	t.Helper()
	raw, err := f.db.Collection("restaurants").FindOne(f.ctx, bson.D{{Key: "_id", Value: store.RestaurantID}}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw.Lookup("seatsInUse").Int32()
}

func run(t *testing.T, f fixture, interval time.Duration) (stop func(), logs *bytes.Buffer) {
	t.Helper()
	logs = &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&ServiceEnd{Store: f.st, Logger: newLogger(logs), Interval: interval}).Run(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not stop")
		}
	}, logs
}

// 시작 직후 순회: 서버가 멈춰 있던 동안 종료 시각이 지난 팀을 바로 처리한다. (간격 1시간이라 첫 순회만 실행)
func TestWorkerCompletesOverdueOnStartup(t *testing.T) {
	f := newFixture(t)
	overdue := f.dining(t, "a@example.com", 3, time.Now().Add(-time.Minute))
	future := f.dining(t, "b@example.com", 2, time.Now().Add(time.Hour))

	stop, _ := run(t, f, time.Hour)
	defer stop()
	waitFor(t, func() bool { return f.status(t, overdue) == "completed" }, 3*time.Second)
	if f.status(t, future) != "dining" || f.seats(t) != 2 {
		t.Fatalf("future party %s seats %d", f.status(t, future), f.seats(t))
	}
}

// 브라우저 요청 없이 주기적 순회만으로 종료 시각이 된 팀을 처리한다.
func TestWorkerCompletesWithoutRequests(t *testing.T) {
	f := newFixture(t)
	soon := f.dining(t, "a@example.com", 4, time.Now().Add(300*time.Millisecond))

	stop, logs := run(t, f, 50*time.Millisecond)
	defer stop()
	if f.status(t, soon) != "dining" {
		t.Fatal("completed before end time")
	}
	waitFor(t, func() bool { return f.status(t, soon) == "completed" }, 3*time.Second)
	if f.seats(t) != 0 {
		t.Fatalf("seatsInUse %d", f.seats(t))
	}
	stop()
	if !bytes.Contains(logs.Bytes(), []byte("service ended")) || bytes.Contains(logs.Bytes(), []byte("@example.com")) {
		t.Fatalf("unexpected logs:\n%s", logs.String())
	}
}

// 서버 종료(ctx 취소) 뒤에는 더 이상 처리하지 않는다.
func TestWorkerStopsOnCancel(t *testing.T) {
	f := newFixture(t)
	stop, _ := run(t, f, 20*time.Millisecond)
	stop()
	later := f.dining(t, "a@example.com", 2, time.Now().Add(-time.Second))
	time.Sleep(150 * time.Millisecond)
	if f.status(t, later) != "dining" || f.seats(t) != 2 {
		t.Fatal("worker processed after being stopped")
	}
}
