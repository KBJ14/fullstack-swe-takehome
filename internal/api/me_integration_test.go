//go:build integration

// GET /api/me를 실제 MongoDB와 함께 검증한다. 식사 중·완료 상태와 좌석은 DB fixture로 준비한다.
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

type meResult struct {
	status       int
	body         string
	cacheControl string
	setCookie    bool
	view         party.StatusView
}

func (e *env) getMe(cookie string) meResult {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/me", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := meResult{
		status:       res.StatusCode,
		body:         string(b),
		cacheControl: res.Header.Get("Cache-Control"),
		setCookie:    len(res.Cookies()) > 0,
	}
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &out.view); err != nil {
			e.t.Fatalf("decode %s: %v", b, err)
		}
	}
	return out
}

// setDining은 체크인 기능 없이 식사 중 상태를 만드는 fixture다.
func (e *env) setDining(cookie string, endsAt time.Time) {
	e.t.Helper()
	now := time.Now().UTC()
	res, err := e.db.Collection("parties").UpdateOne(e.ctx(),
		bson.D{{Key: "tokenHash", Value: token.Hash(cookie)}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: party.StatusDining},
			{Key: "checkedInAt", Value: now},
			{Key: "serviceEndsAt", Value: endsAt},
		}}})
	if err != nil || res.MatchedCount != 1 {
		e.t.Fatalf("dining fixture failed: %v", err)
	}
}

// setSeats는 사용 중 좌석 수를 바꾸는 fixture다.
func (e *env) setSeats(n int) {
	e.t.Helper()
	if _, err := e.db.Collection("restaurants").UpdateOne(e.ctx(),
		bson.D{{Key: "_id", Value: store.RestaurantID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "seatsInUse", Value: n}}}}); err != nil {
		e.t.Fatal(err)
	}
}

// snapshot은 parties와 restaurants의 모든 문서를 원시 BSON으로 모은다. 변경 여부 비교용.
func (e *env) snapshot() []byte {
	e.t.Helper()
	var buf bytes.Buffer
	for _, coll := range []string{"parties", "restaurants"} {
		cur, err := e.db.Collection(coll).Find(e.ctx(), bson.D{}, nil)
		if err != nil {
			e.t.Fatal(err)
		}
		for cur.Next(e.ctx()) {
			buf.Write(cur.Current)
		}
		_ = cur.Close(e.ctx())
	}
	return buf.Bytes()
}

func assertNoSecrets(t *testing.T, body string, cookie string) {
	t.Helper()
	for _, s := range []string{cookie, token.Hash(cookie), "@example.com", "tokenHash", "successorId", "sequence", "emailNormalized", "_id"} {
		if s != "" && strings.Contains(body, s) {
			t.Fatalf("response contains internal value %q: %s", s, body)
		}
	}
}

// --- 시나리오 ---

func TestMeWithIssuedCookie(t *testing.T) {
	e := newEnv(t)
	reg := e.post(reqBody("Kim", "kim@example.com", 2), "")

	r := e.getMe(reg.cookie)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %s", r.status, r.body)
	}
	v := r.view
	if v.Party.Name != "Kim" || v.Party.PartySize != 2 || v.Party.Status != party.StatusWaiting ||
		v.Waiting == nil || v.Waiting.AheadCount != 0 || !v.Waiting.CanCheckIn || v.Dining != nil {
		t.Fatalf("unexpected view %s", r.body)
	}
	if r.cacheControl != "no-store" || r.setCookie {
		t.Fatalf("cache-control %q setCookie %v", r.cacheControl, r.setCookie)
	}
	assertNoSecrets(t, r.body, reg.cookie)
}

func TestMeUnauthorized(t *testing.T) {
	e := newEnv(t)
	e.post(reqBody("Kim", "kim@example.com", 2), "")
	unknown, _ := token.Generate()
	for name, c := range map[string]string{"no cookie": "", "malformed": "not-a-token", "unknown token": unknown} {
		r := e.getMe(c)
		if r.status != http.StatusUnauthorized || !strings.Contains(r.body, "등록 정보를 확인할 수 없습니다.") ||
			strings.Contains(r.body, "Kim") || r.cacheControl != "no-store" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

func TestMeAheadCountExcludesDiningAndCompleted(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 1), "")
	b := e.post(reqBody("B", "b@example.com", 1), "")
	c := e.post(reqBody("C", "c@example.com", 1), "")
	d := e.post(reqBody("D", "d@example.com", 1), "")

	if got := e.getMe(d.cookie).view.Waiting.AheadCount; got != 3 {
		t.Fatalf("D ahead before fixtures: %d", got)
	}

	e.setDining(a.cookie, time.Now().Add(time.Minute)) // A: 식사 중
	e.setSeats(1)
	e.setStatus(b.cookie, party.StatusCompleted) // B: 완료

	cv := e.getMe(c.cookie).view
	dv := e.getMe(d.cookie).view
	if cv.Waiting.AheadCount != 0 || !cv.Waiting.CanCheckIn {
		t.Fatalf("C: %+v", cv.Waiting)
	}
	if dv.Waiting.AheadCount != 1 || dv.Waiting.CanCheckIn {
		t.Fatalf("D: %+v", dv.Waiting)
	}
}

func TestMeCanCheckInRules(t *testing.T) {
	e := newEnv(t)
	head := e.post(reqBody("Head", "h@example.com", 5), "")
	small := e.post(reqBody("Small", "s@example.com", 2), "")

	// 빈 좌석 4석: 맨 앞 5명은 불가, 뒤 2명은 좌석이 충분해도 순서 때문에 불가
	e.setSeats(6)
	hv, sv := e.getMe(head.cookie).view, e.getMe(small.cookie).view
	if hv.Waiting.AheadCount != 0 || hv.Waiting.CanCheckIn {
		t.Fatalf("head with 4 free seats: %+v", hv.Waiting)
	}
	if sv.Waiting.AheadCount != 1 || sv.Waiting.CanCheckIn {
		t.Fatalf("small behind head: %+v", sv.Waiting)
	}

	// 빈 좌석 5석: 맨 앞 5명 가능, 뒤 팀은 여전히 불가
	e.setSeats(5)
	hv, sv = e.getMe(head.cookie).view, e.getMe(small.cookie).view
	if !hv.Waiting.CanCheckIn || sv.Waiting.CanCheckIn {
		t.Fatalf("5 free seats: head %+v small %+v", hv.Waiting, sv.Waiting)
	}
}

func TestMeDiningReturnsServiceEnd(t *testing.T) {
	e := newEnv(t)
	reg := e.post(reqBody("Kim", "kim@example.com", 2), "")
	endsAt := time.Now().UTC().Add(6 * time.Second).Truncate(time.Millisecond)
	e.setDining(reg.cookie, endsAt)

	before := time.Now().UTC().Add(-time.Second)
	r := e.getMe(reg.cookie)
	after := time.Now().UTC().Add(time.Second)
	if r.status != http.StatusOK || r.view.Party.Status != party.StatusDining || r.view.Dining == nil || r.view.Waiting != nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !r.view.Dining.ServiceEndsAt.Equal(endsAt) {
		t.Fatalf("serviceEndsAt %v want %v", r.view.Dining.ServiceEndsAt, endsAt)
	}
	if st := r.view.Dining.ServerTime; st.Before(before) || st.After(after) {
		t.Fatalf("serverTime %v outside [%v, %v]", st, before, after)
	}
}

func TestMeCompletedReturns200(t *testing.T) {
	e := newEnv(t)
	reg := e.post(reqBody("Kim", "kim@example.com", 2), "")
	e.setStatus(reg.cookie, party.StatusCompleted)

	r := e.getMe(reg.cookie)
	if r.status != http.StatusOK || r.view.Party.Status != party.StatusCompleted || r.view.Waiting != nil || r.view.Dining != nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if strings.Contains(r.body, `"waiting"`) || strings.Contains(r.body, `"dining"`) {
		t.Fatalf("completed body must contain only party: %s", r.body)
	}
}

// 재등록 201 응답을 받지 못해 이전 완료 쿠키만 남은 상황.
// 조회와 등록이 같은 연결 규칙(successor 한 단계)으로 해석하는지 상태 변화에 따라 확인한다.
func TestMeFollowsSuccessorConsistentlyWithRegister(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)

	// A의 쿠키로 재등록. 응답의 새 쿠키는 브라우저에 저장되지 않았다고 가정하고 버린다.
	lost := e.post(reqBody("B", "b@example.com", 3), a.cookie)
	if lost.status != http.StatusCreated {
		t.Fatalf("re-register: %d", lost.status)
	}

	// 1) successor 대기 중: 조회는 B를 보여 주고, 등록은 B를 200으로 돌려준다.
	r := e.getMe(a.cookie)
	if r.status != http.StatusOK || r.view.Party.Name != "B" || r.view.Party.Status != party.StatusWaiting || r.view.Waiting == nil {
		t.Fatalf("waiting successor via old cookie: %d %s", r.status, r.body)
	}
	if again := e.post(reqBody("X", "x@example.com", 1), a.cookie); again.status != http.StatusOK {
		t.Fatalf("register with old cookie while successor waiting: %d", again.status)
	}
	// 새 쿠키로 조회해도 같은 팀이다.
	if bv := e.getMe(lost.cookie).view; bv.Party.Name != "B" {
		t.Fatalf("new cookie: %+v", bv)
	}

	// 2) successor 식사 중: 조회는 B의 식사 정보를 보여 준다.
	e.setDining(lost.cookie, time.Now().Add(time.Minute))
	if r := e.getMe(a.cookie); r.view.Party.Name != "B" || r.view.Dining == nil {
		t.Fatalf("dining successor via old cookie: %s", r.body)
	}

	// 3) successor 완료: 조회는 B의 완료 상태(200), 등록은 쿠키 없는 요청처럼 새 팀을 허용
	e.setStatus(lost.cookie, party.StatusCompleted)
	r = e.getMe(a.cookie)
	if r.status != http.StatusOK || r.view.Party.Name != "B" || r.view.Party.Status != party.StatusCompleted {
		t.Fatalf("completed successor via old cookie: %d %s", r.status, r.body)
	}
	c := e.post(reqBody("C", "c@example.com", 1), a.cookie)
	if c.status != http.StatusCreated {
		t.Fatalf("register with stale cookie: %d", c.status)
	}
	// 오래된 쿠키는 여전히 B(완료)를 가리키고, 새 팀 C는 새 쿠키로만 조회된다.
	if r := e.getMe(a.cookie); r.view.Party.Name != "B" {
		t.Fatalf("stale cookie after new registration: %s", r.body)
	}
	if r := e.getMe(c.cookie); r.view.Party.Name != "C" || r.view.Party.Status != party.StatusWaiting {
		t.Fatalf("new party cookie: %s", r.body)
	}
}

func TestBrokenSuccessorReferenceIsAnErrorNotUnregistered(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	missing := bson.NewObjectID()
	if _, err := e.db.Collection("parties").UpdateOne(e.ctx(),
		bson.D{{Key: "tokenHash", Value: token.Hash(a.cookie)}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: party.StatusCompleted}, {Key: "successorId", Value: missing}}}}); err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()

	r := e.getMe(a.cookie)
	if r.status != http.StatusInternalServerError || !strings.Contains(r.body, `"internal_error"`) ||
		strings.Contains(r.body, missing.Hex()) || strings.Contains(r.body, "successor") {
		t.Fatalf("GET with broken reference: %d %s", r.status, r.body)
	}

	// 등록 API도 같은 해석을 하므로, 끊어진 참조를 미등록으로 보고 새 팀을 만들지 않는다.
	reg := e.post(reqBody("A2", "a2@example.com", 2), a.cookie)
	if reg.status != http.StatusInternalServerError || reg.cookie != "" {
		t.Fatalf("POST with broken reference: %d %s", reg.status, reg.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("data changed")
	}
}

func TestMeDoesNotModifyAnything(t *testing.T) {
	e := newEnv(t)
	waiting := e.post(reqBody("W", "w@example.com", 2), "")
	dining := e.post(reqBody("D", "d@example.com", 3), "")
	done := e.post(reqBody("C", "c@example.com", 1), "")
	// 종료 예정 시각이 이미 지난 식사 중 팀: 조회가 종료 처리를 하면 안 된다.
	e.setDining(dining.cookie, time.Now().Add(-time.Minute))
	e.setSeats(3)
	e.setStatus(done.cookie, party.StatusCompleted)

	before := e.snapshot()
	for i := 0; i < 3; i++ {
		for _, c := range []string{waiting.cookie, dining.cookie, done.cookie} {
			r := e.getMe(c)
			if r.status != http.StatusOK || r.setCookie {
				t.Fatalf("status %d setCookie %v", r.status, r.setCookie)
			}
			assertNoSecrets(t, r.body, c)
		}
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("GET /api/me changed stored data")
	}
	if r := e.getMe(dining.cookie); r.view.Party.Status != party.StatusDining {
		t.Fatal("past-due dining party must stay dining until the service-end job exists")
	}
	// 조회 후에도 원래 쿠키가 그대로 유효하다.
	if e.getMe(waiting.cookie).status != http.StatusOK {
		t.Fatal("cookie invalidated by GET")
	}
}
