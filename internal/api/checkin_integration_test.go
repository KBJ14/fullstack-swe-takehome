//go:build integration

// POST /api/me/check-in을 실제 MongoDB와 함께 검증한다.
// 식사 중·완료 상태와 좌석은 DB fixture로 준비한다. (식사 종료 작업은 구현하지 않음)
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

type checkInResp struct {
	status  int
	body    string
	errCode string
	result  string
	view    party.StatusView
	headers http.Header
	cookies int
}

func (e *env) checkIn(cookie string, mutate func(*http.Request)) checkInResp {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/me/check-in", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	if mutate != nil {
		mutate(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Error(err)
		return checkInResp{}
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := checkInResp{status: res.StatusCode, body: string(b), headers: res.Header, cookies: len(res.Cookies())}
	if res.StatusCode == http.StatusOK {
		var v struct {
			Result string `json:"result"`
			party.StatusView
		}
		_ = json.Unmarshal(b, &v)
		out.result, out.view = v.Result, v.StatusView
	} else {
		var er errResp
		_ = json.Unmarshal(b, &er)
		out.errCode = er.Error.Code
	}
	return out
}

func docTime(t *testing.T, v any) time.Time {
	t.Helper()
	dt, ok := v.(bson.DateTime)
	if !ok {
		t.Fatalf("not a date: %T %v", v, v)
	}
	return dt.Time().UTC()
}

func seatsInUse(e *env) int64 { return asInt64(e.restaurant()["seatsInUse"]) }

// --- 시나리오 ---

func TestCheckInSuccessWithIssuedCookie(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 4), "")
	b := e.post(reqBody("B", "b@example.com", 2), "")

	before := time.Now().UTC().Add(-time.Second)
	r := e.checkIn(a.cookie, nil)
	after := time.Now().UTC().Add(time.Second)
	if r.status != http.StatusOK || r.result != "checked_in" || r.view.Party.Status != party.StatusDining || r.view.Dining == nil {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r.headers.Get("Cache-Control") != "no-store" || r.cookies != 0 {
		t.Fatal("check-in must be no-store and must not set cookies")
	}
	assertNoSecrets(t, r.body, a.cookie)

	doc := e.partyByCookie(a.cookie)
	checkedInAt, endsAt := docTime(t, doc["checkedInAt"]), docTime(t, doc["serviceEndsAt"])
	if doc["status"] != "dining" || checkedInAt.Before(before) || checkedInAt.After(after) {
		t.Fatalf("stored doc %v", doc)
	}
	// 인원당 3초: 4명 → 12초
	if endsAt.Sub(checkedInAt) != 12*time.Second {
		t.Fatalf("service duration %v", endsAt.Sub(checkedInAt))
	}
	// 응답의 종료 시각은 커밋된 값과 같다.
	if !r.view.Dining.ServiceEndsAt.Equal(endsAt) {
		t.Fatalf("response endsAt %v stored %v", r.view.Dining.ServiceEndsAt, endsAt)
	}
	if got := seatsInUse(e); got != 4 {
		t.Fatalf("seatsInUse %d", got)
	}

	// 체크인 후 조회: A는 식사 중, B는 대기열 맨 앞이 되어 체크인 가능 (빈 좌석 6 ≥ 2)
	if av := e.getMe(a.cookie).view; av.Party.Status != party.StatusDining || !av.Dining.ServiceEndsAt.Equal(endsAt) {
		t.Fatalf("A after check-in: %+v", av)
	}
	bv := e.getMe(b.cookie).view
	if bv.Waiting == nil || bv.Waiting.AheadCount != 0 || !bv.Waiting.CanCheckIn {
		t.Fatalf("B after A check-in: %+v", bv.Waiting)
	}
}

func TestCheckInUnauthorized(t *testing.T) {
	e := newEnv(t)
	e.post(reqBody("A", "a@example.com", 2), "")
	unknown, _ := token.Generate()
	before := e.snapshot()
	for name, c := range map[string]string{"no cookie": "", "malformed": "garbage", "unknown token": unknown} {
		r := e.checkIn(c, nil)
		if r.status != http.StatusUnauthorized || r.errCode != "not_registered" || r.headers.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("data changed")
	}
}

func TestCheckInBehindHeadIsRejectedEvenWithSeats(t *testing.T) {
	e := newEnv(t)
	e.post(reqBody("Head", "h@example.com", 5), "")
	small := e.post(reqBody("Small", "s@example.com", 1), "")
	before := e.snapshot()

	r := e.checkIn(small.cookie, nil)
	if r.status != http.StatusConflict || r.errCode != "not_your_turn" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("rejected check-in changed data")
	}
}

func TestCheckInHeadWithoutEnoughSeatsLeavesNoPartialChanges(t *testing.T) {
	e := newEnv(t)
	head := e.post(reqBody("Head", "h@example.com", 5), "")
	e.post(reqBody("Small", "s@example.com", 2), "")
	e.setSeats(6) // 빈 좌석 4석
	before := e.snapshot()

	// 팀 문서를 dining으로 바꾼 뒤 좌석 확보에서 실패하는 경로: 트랜잭션 중단으로 팀 변경도 취소되어야 한다.
	r := e.checkIn(head.cookie, nil)
	if r.status != http.StatusConflict || r.errCode != "not_enough_seats" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("failed check-in left partial changes")
	}
	doc := e.partyByCookie(head.cookie)
	if doc["status"] != "waiting" || doc["checkedInAt"] != nil || doc["serviceEndsAt"] != nil {
		t.Fatalf("party changed: %v", doc)
	}

	// 좌석이 생기면 같은 요청이 성공한다.
	e.setSeats(5)
	if r := e.checkIn(head.cookie, nil); r.status != http.StatusOK || r.result != "checked_in" {
		t.Fatalf("after seats freed: %d %s", r.status, r.body)
	}
	if got := seatsInUse(e); got != 10 {
		t.Fatalf("seatsInUse %d", got)
	}
}

func TestCheckInTenPartyInEmptyRestaurant(t *testing.T) {
	e := newEnv(t)
	ten := e.post(reqBody("Ten", "t@example.com", 10), "")
	one := e.post(reqBody("One", "o@example.com", 1), "")

	r := e.checkIn(ten.cookie, nil)
	if r.status != http.StatusOK || r.result != "checked_in" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got := seatsInUse(e); got != 10 {
		t.Fatalf("seatsInUse %d", got)
	}
	doc := e.partyByCookie(ten.cookie)
	if d := docTime(t, doc["serviceEndsAt"]).Sub(docTime(t, doc["checkedInAt"])); d != 30*time.Second {
		t.Fatalf("10 people service duration %v", d)
	}
	// 만석: 다음 팀은 맨 앞이지만 좌석 부족
	if r := e.checkIn(one.cookie, nil); r.status != http.StatusConflict || r.errCode != "not_enough_seats" {
		t.Fatalf("next party in full restaurant: %d %s", r.status, r.body)
	}
	if got := seatsInUse(e); got != 10 {
		t.Fatalf("seatsInUse %d", got)
	}
}

func TestCheckInRepeatedSequentiallyIsIdempotent(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 3), "")
	first := e.checkIn(a.cookie, nil)
	stored := e.partyByCookie(a.cookie)

	for i := 0; i < 3; i++ {
		r := e.checkIn(a.cookie, nil)
		if r.status != http.StatusOK || r.result != "already_dining" {
			t.Fatalf("repeat %d: %d %s", i, r.status, r.body)
		}
		if !r.view.Dining.ServiceEndsAt.Equal(first.view.Dining.ServiceEndsAt) {
			t.Fatal("service end time changed on repeat")
		}
	}
	if got := seatsInUse(e); got != 3 {
		t.Fatalf("seats deducted more than once: %d", got)
	}
	again := e.partyByCookie(a.cookie)
	if !docTime(t, again["checkedInAt"]).Equal(docTime(t, stored["checkedInAt"])) ||
		!docTime(t, again["serviceEndsAt"]).Equal(docTime(t, stored["serviceEndsAt"])) {
		t.Fatal("stored times changed on repeat")
	}
}

func TestCheckInConcurrentSameParty(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 4), "")

	const n = 15
	results := make([]checkInResp, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.checkIn(a.cookie, nil)
		}(i)
	}
	wg.Wait()

	started := 0
	var endsAt time.Time
	for _, r := range results {
		if r.status != http.StatusOK {
			t.Fatalf("unexpected %d %s", r.status, r.body)
		}
		if r.result == "checked_in" {
			started++
		}
		if endsAt.IsZero() {
			endsAt = r.view.Dining.ServiceEndsAt
		} else if !r.view.Dining.ServiceEndsAt.Equal(endsAt) {
			t.Fatal("responses disagree on service end time")
		}
	}
	if started != 1 {
		t.Fatalf("checked_in %d times", started)
	}
	if got := seatsInUse(e); got != 4 {
		t.Fatalf("seatsInUse %d", got)
	}
	if stored := docTime(t, e.partyByCookie(a.cookie)["serviceEndsAt"]); !stored.Equal(endsAt) {
		t.Fatalf("stored %v responses %v", stored, endsAt)
	}
}

func TestCheckInAlreadyDiningReturnsStoredTimes(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	// 종료 예정 시각이 이미 지난 식사 중 fixture: 체크인 API가 완료 처리하면 안 된다.
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	e.setDining(a.cookie, past)
	e.setSeats(2)
	before := e.snapshot()

	r := e.checkIn(a.cookie, nil)
	if r.status != http.StatusOK || r.result != "already_dining" || !r.view.Dining.ServiceEndsAt.Equal(past) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("already-dining check-in changed data")
	}
}

func TestCheckInCompletedIsRejected(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)
	before := e.snapshot()

	r := e.checkIn(a.cookie, nil)
	if r.status != http.StatusConflict || r.errCode != "already_completed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("completed party restarted")
	}
}

func TestCheckInFollowsSuccessorRule(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	e.setStatus(a.cookie, party.StatusCompleted)
	b := e.post(reqBody("B", "b@example.com", 3), a.cookie) // 재등록 (201 응답의 쿠키는 잃어버렸다고 가정)

	// 이전 완료 쿠키로 체크인 → successor B가 체크인된다.
	r := e.checkIn(a.cookie, nil)
	if r.status != http.StatusOK || r.result != "checked_in" || r.view.Party.Name != "B" {
		t.Fatalf("check-in via old cookie: %d %s", r.status, r.body)
	}
	if e.partyByCookie(b.cookie)["status"] != "dining" || e.partyByCookie(a.cookie)["status"] != "completed" {
		t.Fatal("wrong party checked in")
	}
	if got := seatsInUse(e); got != 3 {
		t.Fatalf("seatsInUse %d", got)
	}

	// successor까지 완료되면 이전 쿠키의 체크인은 409 (식사 재시작 없음)
	e.setStatus(b.cookie, party.StatusCompleted)
	before := e.snapshot()
	if r := e.checkIn(a.cookie, nil); r.status != http.StatusConflict || r.errCode != "already_completed" {
		t.Fatalf("completed successor: %d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("data changed")
	}
}

func TestCheckInBrokenSuccessorIsInternalError(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	missing := bson.NewObjectID()
	if _, err := e.db.Collection("parties").UpdateOne(e.ctx(),
		bson.D{{Key: "tokenHash", Value: token.Hash(a.cookie)}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: party.StatusCompleted}, {Key: "successorId", Value: missing}}}}); err != nil {
		t.Fatal(err)
	}
	before := e.snapshot()
	r := e.checkIn(a.cookie, nil)
	if r.status != http.StatusInternalServerError || r.errCode != "internal_error" ||
		strings.Contains(r.body, missing.Hex()) || strings.Contains(r.body, "successor") {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("data changed")
	}
}

func TestCheckInCSRFRejectedChangesNothing(t *testing.T) {
	e := newEnv(t)
	a := e.post(reqBody("A", "a@example.com", 2), "")
	before := e.snapshot()
	r := e.checkIn(a.cookie, func(req *http.Request) { req.Header.Set("Sec-Fetch-Site", "cross-site") })
	if r.status != http.StatusForbidden || r.errCode != "forbidden" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if !bytes.Equal(before, e.snapshot()) {
		t.Fatal("CSRF-rejected request changed data")
	}
}

// 서로 다른 팀이 동시에 체크인을 요청한다.
//
// 같은 라운드 안에서는 뒤 팀이 앞 팀의 커밋 전 상태를 보고 409(not_your_turn)를 받을 수 있다.
// 따라서 "좌석이 충분하면 모두 즉시 성공"을 기대하지 않고, 진행이 멈출 때까지 대기 중인 팀이 다시 요청하게 한다.
// 매 라운드 후 확인하는 불변 조건:
//   - 응답은 200 또는 409뿐 (500 없음)
//   - 사용 중 좌석 = 식사 중 팀 인원 합 ≤ 10
//   - 식사 중 팀은 항상 등록 순서의 앞부분(prefix) — 추월 없음
func TestCheckInConcurrentDifferentPartiesKeepsOrderAndCapacity(t *testing.T) {
	configs := map[string]struct {
		sizes      []int
		wantDining int // 최종적으로 식사 중이어야 하는 앞부분 팀 수
	}{
		// 4+3+3=10, 다음 2명은 좌석 부족
		"mixed sizes": {[]int{4, 3, 3, 2, 5, 1}, 3},
		// 5+5=10
		"two fives": {[]int{5, 5, 1}, 2},
		// 1명 팀 12개 중 10팀까지
		"many ones": {[]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, 10},
		// 6명 입장 후 5명은 좌석 부족, 1명은 추월 불가
		"blocked head": {[]int{6, 5, 1}, 1},
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			cookies := make([]string, len(cfg.sizes))
			for i, s := range cfg.sizes {
				cookies[i] = e.post(reqBody(fmt.Sprintf("P%d", i), fmt.Sprintf("p%d@example.com", i), s), "").cookie
			}

			dining := map[int]bool{}
			for round := 0; ; round++ {
				if round > len(cfg.sizes)+2 {
					t.Fatal("no convergence")
				}
				var mu sync.Mutex
				progressed := false
				var wg sync.WaitGroup
				for i := range cookies {
					if dining[i] {
						continue
					}
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						r := e.checkIn(cookies[i], nil)
						switch {
						case r.status == http.StatusOK && r.result == "checked_in":
							mu.Lock()
							dining[i], progressed = true, true
							mu.Unlock()
						case r.status == http.StatusConflict && (r.errCode == "not_your_turn" || r.errCode == "not_enough_seats"):
						default:
							t.Errorf("round %d party %d: unexpected %d %s", round, i, r.status, r.body)
						}
					}(i)
				}
				wg.Wait()

				// 불변 조건: prefix, 좌석 합, 정원
				sum := 0
				for i := range cookies {
					if dining[i] {
						sum += cfg.sizes[i]
						for j := 0; j < i; j++ {
							if !dining[j] {
								t.Fatalf("round %d: party %d overtook party %d", round, i, j)
							}
						}
					}
				}
				if got := seatsInUse(e); got != int64(sum) || got > 10 {
					t.Fatalf("round %d: seatsInUse %d, dining sum %d", round, got, sum)
				}
				if !progressed {
					break
				}
			}

			if len(dining) != cfg.wantDining {
				t.Fatalf("dining parties %d, want %d", len(dining), cfg.wantDining)
			}
			// 남은 맨 앞 팀은 좌석 부족, 그 뒤 팀은 순서 때문에 거부된다 (최신 상태에서 다시 요청)
			if cfg.wantDining < len(cookies) {
				if r := e.checkIn(cookies[cfg.wantDining], nil); r.errCode != "not_enough_seats" {
					t.Fatalf("remaining head: %d %s", r.status, r.body)
				}
				for i := cfg.wantDining + 1; i < len(cookies); i++ {
					if r := e.checkIn(cookies[i], nil); r.errCode != "not_your_turn" {
						t.Fatalf("party %d behind blocked head: %d %s", i, r.status, r.body)
					}
				}
			}
		})
	}
}
