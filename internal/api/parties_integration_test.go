//go:build integration

// POST /api/parties를 실제 MongoDB replica set과 함께 검증하는 통합 테스트.
//
// 각 테스트는 고유한 임시 데이터베이스를 만들고, 끝나면 그 데이터베이스만 삭제한다.
// 식사 중·완료 상태는 체크인·종료 기능 없이 DB fixture로 준비한다.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/KBJ14/fullstack-swe-takehome/internal/database"
	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

type env struct {
	t   *testing.T
	srv *httptest.Server
	db  *mongo.Database
	st  *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	client, err := database.Connect(uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	name := "it_api_" + hex.EncodeToString(buf)
	db := client.Database(name)
	t.Cleanup(func() {
		if err := db.Drop(context.Background()); err != nil {
			t.Errorf("drop temp database: %v", err)
		}
	})

	st := store.New(client, name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(New(st, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, db: db, st: st}
}

type response struct {
	status int
	cookie string // 새로 발급된 식별 쿠키 값 (없으면 빈 값)
	body   string
}

func (r response) result() (string, party.View) {
	var b struct {
		Result string     `json:"result"`
		Party  party.View `json:"party"`
	}
	_ = json.Unmarshal([]byte(r.body), &b)
	return b.Result, b.Party
}

func reqBody(name, email string, size int) string {
	b, _ := json.Marshal(map[string]any{"name": name, "email": email, "partySize": size})
	return string(b)
}

func (e *env) post(body, cookie string) response {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/parties", strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Error(err)
		return response{}
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := response{status: res.StatusCode, body: string(b)}
	for _, c := range res.Cookies() {
		if c.Name == CookieName {
			out.cookie = c.Value
		}
	}
	return out
}

func (e *env) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	e.t.Cleanup(cancel)
	return ctx
}

func (e *env) count(filter bson.D) int64 {
	e.t.Helper()
	n, err := e.db.Collection("parties").CountDocuments(e.ctx(), filter)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) activeCount() int64 {
	return e.count(bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{"waiting", "dining"}}}}})
}

func (e *env) partyByCookie(cookie string) bson.M {
	e.t.Helper()
	var doc bson.M
	if err := e.db.Collection("parties").FindOne(e.ctx(), bson.D{{Key: "tokenHash", Value: token.Hash(cookie)}}).Decode(&doc); err != nil {
		e.t.Fatalf("party for cookie not found: %v", err)
	}
	return doc
}

// setStatus는 체크인·종료 기능 없이 팀 상태를 바꾸는 fixture다.
func (e *env) setStatus(cookie string, st party.Status) {
	e.t.Helper()
	res, err := e.db.Collection("parties").UpdateOne(e.ctx(),
		bson.D{{Key: "tokenHash", Value: token.Hash(cookie)}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: st}}}})
	if err != nil || res.MatchedCount != 1 {
		e.t.Fatalf("fixture update failed: %v", err)
	}
}

func (e *env) restaurant() bson.M {
	e.t.Helper()
	var doc bson.M
	if err := e.db.Collection("restaurants").FindOne(e.ctx(), bson.D{{Key: "_id", Value: store.RestaurantID}}).Decode(&doc); err != nil {
		e.t.Fatal(err)
	}
	return doc
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	}
	panic(fmt.Sprintf("unexpected number type %T", v))
}

// --- 시나리오 ---

func TestRegisterCreatesPartyAndCookieIdentifiesIt(t *testing.T) {
	e := newEnv(t)
	r := e.post(reqBody("  Kim  ", "  Kim@Example.COM ", 2), "")

	if r.status != http.StatusCreated || r.cookie == "" {
		t.Fatalf("status %d cookie %q body %s", r.status, r.cookie, r.body)
	}
	result, view := r.result()
	if result != "created" || view.Name != "Kim" || view.PartySize != 2 || view.Status != party.StatusWaiting {
		t.Fatalf("unexpected body %s", r.body)
	}
	if strings.Contains(r.body, r.cookie) {
		t.Fatal("token in response body")
	}

	doc := e.partyByCookie(r.cookie) // 발급 쿠키로 팀을 식별할 수 있다
	if doc["status"] != "waiting" || doc["emailNormalized"] != "kim@example.com" || doc["email"] != "Kim@Example.COM" ||
		doc["name"] != "Kim" || asInt64(doc["sequence"]) != 1 {
		t.Fatalf("unexpected stored doc %v", doc)
	}
	// DB 어디에도 토큰 원문이 없다.
	raw, _ := bson.Marshal(doc)
	if strings.Contains(string(raw), r.cookie) {
		t.Fatal("raw token stored in DB")
	}

	rest := e.restaurant()
	if asInt64(rest["seatsInUse"]) != 0 || asInt64(rest["nextSequence"]) != 1 || asInt64(rest["capacity"]) != store.Capacity {
		t.Fatalf("unexpected restaurant %v", rest)
	}
}

func TestPartySizeBoundariesStored(t *testing.T) {
	e := newEnv(t)
	for i, n := range []int{1, 10} {
		r := e.post(reqBody("P", fmt.Sprintf("p%d@example.com", i), n), "")
		if r.status != http.StatusCreated {
			t.Fatalf("partySize %d: status %d", n, r.status)
		}
		if got := asInt64(e.partyByCookie(r.cookie)["partySize"]); got != int64(n) {
			t.Fatalf("stored partySize %d", got)
		}
	}
}

func TestInvalidInputCreatesNothing(t *testing.T) {
	e := newEnv(t)
	for _, b := range []string{
		`{"name":"","email":"a@example.com","partySize":2}`,
		`{"name":"Kim","email":"abc","partySize":2}`,
		`{"name":"Kim","email":"a@example.com","partySize":0}`,
		`{"name":"Kim","email":"a@example.com","partySize":11}`,
		`{"name":"Kim","email":"a@example.com","partySize":2.5}`,
		`{"name":"Kim","email":"a@example.com","partySize":"2"}`,
	} {
		if r := e.post(b, ""); r.status != http.StatusBadRequest || r.cookie != "" {
			t.Errorf("%s: status %d", b, r.status)
		}
	}
	if n := e.count(bson.D{}); n != 0 {
		t.Fatalf("expected no parties, got %d", n)
	}
	if asInt64(e.restaurant()["nextSequence"]) != 0 {
		t.Fatal("sequence must not advance on invalid input")
	}
}

func TestSameEmailWithoutCookieIs409AndHidesParty(t *testing.T) {
	e := newEnv(t)
	first := e.post(reqBody("Kim", "a@example.com", 2), "")
	if first.status != http.StatusCreated {
		t.Fatal(first.body)
	}

	r := e.post(reqBody("Other", "  A@EXAMPLE.com ", 3), "")
	if r.status != http.StatusConflict || r.cookie != "" {
		t.Fatalf("status %d", r.status)
	}
	if !strings.Contains(r.body, "이 이메일로 진행 중인 등록이 있습니다. 등록했던 브라우저에서 확인해 주세요.") {
		t.Fatalf("unexpected message %s", r.body)
	}
	if strings.Contains(r.body, "Kim") || strings.Contains(r.body, "party") {
		t.Fatalf("409 leaked existing party info: %s", r.body)
	}
	if e.count(bson.D{}) != 1 {
		t.Fatal("duplicate party created")
	}
}

func TestConcurrentSameEmailCreatesOneActiveParty(t *testing.T) {
	e := newEnv(t)
	// 패배한 요청이 재시도 끝에 500이 아니라 409가 되는지 확인하기 위해 충분히 많이 보낸다.
	const n = 30
	emails := []string{"dup@example.com", "DUP@example.com", " Dup@Example.Com "}
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = e.post(reqBody("P", emails[i%len(emails)], 2), "").status
		}(i)
	}
	wg.Wait()

	created, conflict := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if created != 1 || conflict != n-1 {
		t.Fatalf("created=%d conflict=%d", created, conflict)
	}
	if e.activeCount() != 1 || e.count(bson.D{}) != 1 {
		t.Fatal("expected exactly one party")
	}
	if asInt64(e.restaurant()["nextSequence"]) != 1 {
		t.Fatal("aborted registrations must not consume sequence numbers")
	}
}

func TestConcurrentDifferentEmailsGetUniqueSequences(t *testing.T) {
	e := newEnv(t)
	const n = 20
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = e.post(reqBody("P", fmt.Sprintf("u%d@example.com", i), 1+i%10), "").status
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusCreated {
			t.Fatalf("request %d: status %d", i, s)
		}
	}

	cur, err := e.db.Collection("parties").Find(e.ctx(), bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	var docs []bson.M
	if err := cur.All(e.ctx(), &docs); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, d := range docs {
		seq := asInt64(d["sequence"])
		if seen[seq] || seq < 1 || seq > n {
			t.Fatalf("bad or duplicate sequence %d", seq)
		}
		seen[seq] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d sequences, got %d", n, len(seen))
	}
	rest := e.restaurant()
	if asInt64(rest["nextSequence"]) != n {
		t.Fatalf("nextSequence %v", rest["nextSequence"])
	}
	// 등록은 좌석을 차감하지 않는다.
	if asInt64(rest["seatsInUse"]) != 0 {
		t.Fatalf("seatsInUse changed: %v", rest["seatsInUse"])
	}
}

func TestValidCookieReturnsExistingAndIgnoresNewInput(t *testing.T) {
	e := newEnv(t)
	first := e.post(reqBody("Kim", "a@example.com", 2), "")

	// 같은 입력 반복
	again := e.post(reqBody("Kim", "a@example.com", 2), first.cookie)
	if again.status != http.StatusOK || again.cookie != "" {
		t.Fatalf("repeat: status %d cookie %q", again.status, again.cookie)
	}
	if res, v := again.result(); res != "existing" || v.Name != "Kim" || v.PartySize != 2 {
		t.Fatalf("repeat body %s", again.body)
	}

	// 다른 이메일·이름·인원수
	other := e.post(reqBody("Lee", "b@example.com", 5), first.cookie)
	if other.status != http.StatusOK || other.cookie != "" {
		t.Fatalf("different input: status %d", other.status)
	}
	if res, v := other.result(); res != "existing" || v.Name != "Kim" || v.PartySize != 2 {
		t.Fatalf("different input body %s", other.body)
	}

	if e.count(bson.D{}) != 1 || e.count(bson.D{{Key: "emailNormalized", Value: "b@example.com"}}) != 0 {
		t.Fatal("cookie holder must not create another party")
	}
}

func TestDiningCookieReturnsExisting(t *testing.T) {
	e := newEnv(t)
	first := e.post(reqBody("Kim", "a@example.com", 2), "")
	e.setStatus(first.cookie, party.StatusDining)

	r := e.post(reqBody("Kim", "c@example.com", 2), first.cookie)
	if res, v := r.result(); r.status != http.StatusOK || res != "existing" || v.Status != party.StatusDining {
		t.Fatalf("status %d body %s", r.status, r.body)
	}
	// 식사 중인 이메일은 쿠키 없이 재등록할 수 없다.
	if r := e.post(reqBody("Kim", "a@example.com", 2), ""); r.status != http.StatusConflict {
		t.Fatalf("dining email without cookie: %d", r.status)
	}
	if e.count(bson.D{}) != 1 {
		t.Fatal("unexpected new party")
	}
}

func TestCompletedCookieConcurrentReRegisterCreatesOneParty(t *testing.T) {
	e := newEnv(t)
	first := e.post(reqBody("Kim", "a@example.com", 2), "")
	e.setStatus(first.cookie, party.StatusCompleted)

	const n = 10
	results := make([]response, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 요청마다 다른 이메일이라 이메일 고유 인덱스만으로는 막을 수 없는 경우
			results[i] = e.post(reqBody(fmt.Sprintf("R%d", i), fmt.Sprintf("r%d@example.com", i), 2), first.cookie)
		}(i)
	}
	wg.Wait()

	var createdName, winnerCookie string
	created, existing := 0, 0
	for _, r := range results {
		switch r.status {
		case http.StatusCreated:
			created++
			_, v := r.result()
			createdName = v.Name
			winnerCookie = r.cookie
			if r.cookie == "" {
				t.Error("created response without cookie")
			}
		case http.StatusOK:
			existing++
			if r.cookie != "" {
				t.Error("existing response must not set cookie")
			}
		default:
			t.Errorf("unexpected status %d %s", r.status, r.body)
		}
	}
	if created != 1 || existing != n-1 {
		t.Fatalf("created=%d existing=%d", created, existing)
	}
	for _, r := range results {
		if _, v := r.result(); r.status == http.StatusOK && v.Name != createdName {
			t.Fatalf("existing response points to a different party: %s", r.body)
		}
	}
	if e.activeCount() != 1 || e.count(bson.D{}) != 2 {
		t.Fatalf("active=%d total=%d", e.activeCount(), e.count(bson.D{}))
	}

	pred := e.partyByCookie(first.cookie)
	succ := e.count(bson.D{{Key: "_id", Value: pred["successorId"]}, {Key: "status", Value: "waiting"}})
	if succ != 1 {
		t.Fatal("predecessor must point to the new waiting party")
	}
	// 승리한 응답의 쿠키가 같은 새 팀을 가리킨다.
	if e.partyByCookie(winnerCookie)["_id"] != pred["successorId"] {
		t.Fatal("winner cookie does not identify the successor")
	}
}

// 실제 브라우저처럼 하나의 쿠키 저장소를 공유하는 클라이언트로 동시 재등록한다.
// 응답이 어떤 순서로 도착해도 최종적으로 저장된 쿠키는 새 팀을 가리켜야 한다.
func TestCompletedCookieConcurrentReRegisterBrowserJarEndsWithSuccessorCookie(t *testing.T) {
	e := newEnv(t)
	first := e.post(reqBody("Kim", "a@example.com", 2), "")
	e.setStatus(first.cookie, party.StatusCompleted)

	// 여러 라운드 반복: 매 라운드 브라우저에 남은 쿠키의 팀을 완료시키고 다시 동시 재등록한다.
	start := first.cookie
	for round := 0; round < 5; round++ {
		if round > 0 {
			e.setStatus(start, party.StatusCompleted)
		}
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(e.srv.URL)
		jar.SetCookies(u, []*http.Cookie{{Name: CookieName, Value: start, Path: "/"}})
		client := &http.Client{Jar: jar}

		const n = 8
		statuses := make([]int, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/parties",
					strings.NewReader(reqBody("R", fmt.Sprintf("round%d-%d@example.com", round, i), 2)))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
				res, err := client.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				statuses[i] = res.StatusCode
			}(i)
		}
		wg.Wait()

		created := 0
		for _, s := range statuses {
			switch s {
			case http.StatusCreated:
				created++
			case http.StatusOK:
			default:
				t.Fatalf("round %d: unexpected status %d", round, s)
			}
		}
		if created != 1 {
			t.Fatalf("round %d: created=%d", round, created)
		}

		final := jarCookie(t, jar, u)
		if final == start {
			t.Fatalf("round %d: browser kept the old cookie", round)
		}
		doc := e.partyByCookie(final)
		if doc["status"] != "waiting" || doc["_id"] != e.partyByCookie(start)["successorId"] {
			t.Fatalf("round %d: final browser cookie does not identify the successor: %v", round, doc)
		}
		if e.activeCount() != 1 {
			t.Fatalf("round %d: active=%d", round, e.activeCount())
		}
		start = final
	}
}

// jarCookie는 쿠키 저장소에 최종 저장된 식별 쿠키 값을 돌려준다.
func jarCookie(t *testing.T, jar http.CookieJar, u *url.URL) string {
	t.Helper()
	for _, c := range jar.Cookies(u) {
		if c.Name == CookieName {
			return c.Value
		}
	}
	t.Fatal("no identification cookie in jar")
	return ""
}

// 재등록한 새 팀까지 완료된 뒤 첫 완료 쿠키를 보내면 쿠키가 없는 요청과 같이 처리한다. (docs/design.md 2.9)
func TestStaleCookieAfterSuccessorCompletedActsAsNoCookie(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)
	b := e.post(reqBody("B", "b@example.com", 2), a.cookie) // A의 쿠키로 재등록 → B
	if b.status != http.StatusCreated {
		t.Fatalf("re-register: %d", b.status)
	}
	e.setStatus(b.cookie, party.StatusCompleted)
	succBefore := e.partyByCookie(a.cookie)["successorId"]

	// 진행 중인 다른 팀의 이메일 → 쿠키 없는 요청처럼 409
	e.post(reqBody("X", "x@example.com", 2), "")
	if r := e.post(reqBody("A", "x@example.com", 2), a.cookie); r.status != http.StatusConflict {
		t.Fatalf("stale cookie + taken email: %d", r.status)
	}

	// 사용 가능한 이메일 → 새 팀(201). A의 successorId는 그대로 B를 가리킨다(새로 연결하지 않음).
	r := e.post(reqBody("C", "c@example.com", 2), a.cookie)
	if r.status != http.StatusCreated || r.cookie == "" {
		t.Fatalf("stale cookie + free email: %d", r.status)
	}
	if e.partyByCookie(a.cookie)["successorId"] != succBefore {
		t.Fatal("stale predecessor must not be re-linked")
	}
	if _, ok := e.partyByCookie(r.cookie)["successorId"]; ok {
		t.Fatal("new party must not have successor")
	}
	if e.activeCount() != 2 { // X와 C
		t.Fatalf("active=%d", e.activeCount())
	}
}

func TestReRegisterSameEmailAfterCompletion(t *testing.T) {
	e := newEnv(t)

	// 쿠키 없이: 완료 팀의 이메일로 새 등록
	a := e.post(reqBody("Kim", "a@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)
	r := e.post(reqBody("Kim", "A@example.com", 3), "")
	if r.status != http.StatusCreated || r.cookie == "" || r.cookie == a.cookie {
		t.Fatalf("without cookie: status %d", r.status)
	}

	// 완료 팀의 쿠키로: 같은 이메일 재등록
	b := e.post(reqBody("Lee", "b@example.com", 2), "")
	e.setStatus(b.cookie, party.StatusCompleted)
	r2 := e.post(reqBody("Lee", "b@example.com", 2), b.cookie)
	if r2.status != http.StatusCreated || r2.cookie == "" {
		t.Fatalf("with completed cookie: status %d %s", r2.status, r2.body)
	}
	// 새 쿠키가 새 팀을 가리킨다.
	if doc := e.partyByCookie(r2.cookie); doc["status"] != "waiting" || doc["emailNormalized"] != "b@example.com" {
		t.Fatalf("new cookie points to %v", doc)
	}
	// 오래된 완료 쿠키를 다시 보내면 이미 만든 새 팀을 돌려준다.
	r3 := e.post(reqBody("Lee", "zzz@example.com", 4), b.cookie)
	if res, v := r3.result(); r3.status != http.StatusOK || res != "existing" || v.PartySize != 2 {
		t.Fatalf("old cookie after re-register: %d %s", r3.status, r3.body)
	}
	if e.count(bson.D{}) != 4 || e.activeCount() != 2 {
		t.Fatalf("total=%d active=%d", e.count(bson.D{}), e.activeCount())
	}
}

func TestInvalidCookieGrantsNoAccess(t *testing.T) {
	e := newEnv(t)
	owner := e.post(reqBody("Kim", "a@example.com", 2), "")

	unknown, _ := token.Generate() // 형식은 맞지만 어떤 팀에도 연결되지 않은 토큰

	// 기존 팀의 이메일 + 잘못된 쿠키 → 409, 기존 팀 정보 없음
	r := e.post(reqBody("X", "a@example.com", 2), unknown)
	if r.status != http.StatusConflict || strings.Contains(r.body, "Kim") {
		t.Fatalf("unknown cookie + taken email: %d %s", r.status, r.body)
	}

	// 기존 등록이 없는 정상 입력 + 잘못된 쿠키 → 새 등록 허용
	r = e.post(reqBody("New", "new@example.com", 2), unknown)
	if r.status != http.StatusCreated || r.cookie == "" || r.cookie == unknown {
		t.Fatalf("unknown cookie + new email: %d", r.status)
	}
	r = e.post(reqBody("New2", "new2@example.com", 2), "not-a-valid-token")
	if r.status != http.StatusCreated {
		t.Fatalf("malformed cookie + new email: %d", r.status)
	}

	// 소유자의 팀은 바뀌지 않았다.
	if doc := e.partyByCookie(owner.cookie); doc["name"] != "Kim" || doc["status"] != "waiting" {
		t.Fatalf("owner party changed: %v", doc)
	}
}

func TestFailedTransactionLeavesNoPartialChanges(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	e.post(reqBody("B", "b@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)
	beforeSeq := asInt64(e.restaurant()["nextSequence"])

	// A의 완료 쿠키로 재등록하되, B가 진행 중인 이메일을 사용 → 409
	// 트랜잭션 안에서 선행 팀 표시(successorId)와 순번 증가가 먼저 일어난 뒤 삽입이 실패한다.
	r := e.post(reqBody("A", "B@example.com", 2), a.cookie)
	if r.status != http.StatusConflict {
		t.Fatalf("status %d %s", r.status, r.body)
	}

	if _, ok := e.partyByCookie(a.cookie)["successorId"]; ok {
		t.Fatal("successorId left behind by aborted transaction")
	}
	if got := asInt64(e.restaurant()["nextSequence"]); got != beforeSeq {
		t.Fatalf("sequence advanced by aborted transaction: %d -> %d", beforeSeq, got)
	}
	if e.count(bson.D{}) != 2 {
		t.Fatal("party inserted by aborted transaction")
	}

	// 같은 쿠키로 사용 가능한 이메일이면 정상 재등록된다.
	if r := e.post(reqBody("A", "a2@example.com", 2), a.cookie); r.status != http.StatusCreated {
		t.Fatalf("retry with free email: %d", r.status)
	}
}
