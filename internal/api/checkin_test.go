package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

func postCheckIn(h http.Handler, cookie, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://waitlist.test/api/me/check-in", strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func diningResult(outcome party.CheckInOutcome) party.CheckInResult {
	at := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
	return party.CheckInResult{Outcome: outcome, View: party.StatusView{
		Party:  party.View{Name: "Kim", PartySize: 2, Status: party.StatusDining, RegisteredAt: at},
		Dining: &party.DiningInfo{ServiceEndsAt: at.Add(6 * time.Second), ServerTime: at},
	}}
}

func TestCheckInSuccessResponse(t *testing.T) {
	for _, outcome := range []party.CheckInOutcome{party.CheckInStarted, party.CheckInAlreadyDining} {
		f := &fakeRegistrar{checkIn: diningResult(outcome)}
		tok, _ := token.Generate()
		rec := postCheckIn(newTestHandler(f, Config{}), tok, "", nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", outcome, rec.Code, body)
		}
		for _, want := range []string{
			`"result":"` + string(outcome) + `"`,
			`"status":"dining"`,
			`"dining":{"serviceEndsAt":"2026-11-17T01:02:09Z","serverTime":"2026-11-17T01:02:03Z"}`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body %s missing %s", outcome, body, want)
			}
		}
		if strings.Contains(body, `"waiting"`) || strings.Contains(body, tok) || strings.Contains(body, token.Hash(tok)) {
			t.Errorf("%s: unexpected content %s", outcome, body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: headers %v", outcome, rec.Header())
		}
		if len(f.checkInCalls) != 1 || f.checkInCalls[0] != token.Hash(tok) {
			t.Errorf("%s: store must receive only the token hash", outcome)
		}
	}
}

func TestCheckInErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
		key  string
	}{
		{party.ErrNotRegistered, http.StatusUnauthorized, "not_registered"},
		{party.ErrNotYourTurn, http.StatusConflict, "not_your_turn"},
		{party.ErrNotEnoughSeats, http.StatusConflict, "not_enough_seats"},
		{party.ErrAlreadyCompleted, http.StatusConflict, "already_completed"},
		{errors.New("successorId points to a missing party: party 65abc"), http.StatusInternalServerError, "internal_error"},
	}
	for _, c := range cases {
		f := &fakeRegistrar{checkInErr: c.err}
		tok, _ := token.Generate()
		rec := postCheckIn(newTestHandler(f, Config{}), tok, "", nil)
		if rec.Code != c.code || decodeErr(t, rec).Error.Code != c.key {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "65abc") || strings.Contains(rec.Body.String(), "successor") {
			t.Errorf("internal detail leaked: %s", rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%v: headers", c.err)
		}
	}
}

func TestCheckInWithoutValidCookieIs401(t *testing.T) {
	for name, cookie := range map[string]string{"no cookie": "", "malformed": "garbage"} {
		f := &fakeRegistrar{}
		rec := postCheckIn(newTestHandler(f, Config{}), cookie, "", nil)
		if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).Error.Message != "등록 정보를 확인할 수 없습니다." {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if len(f.checkInCalls) != 0 {
			t.Errorf("%s: store must not be called", name)
		}
	}
}

func TestCheckInRejectsBody(t *testing.T) {
	// 클라이언트가 팀 ID·인원·시각·canCheckIn을 보내도 받아들이지 않는다.
	for _, body := range []string{`{}`, `{"partySize":1}`, `{"canCheckIn":true}`, ` `} {
		f := &fakeRegistrar{checkIn: diningResult(party.CheckInStarted)}
		tok, _ := token.Generate()
		rec := postCheckIn(newTestHandler(f, Config{}), tok, body, func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
		})
		if rec.Code != http.StatusBadRequest || decodeErr(t, rec).Error.Code != "unexpected_body" || len(f.checkInCalls) != 0 {
			t.Errorf("body %q: %d %s", body, rec.Code, rec.Body)
		}
	}
	// 본문이 없으면 Content-Type과 관계없이 처리한다.
	for _, ct := range []string{"", "application/json", "text/plain"} {
		f := &fakeRegistrar{checkIn: diningResult(party.CheckInStarted)}
		tok, _ := token.Generate()
		rec := postCheckIn(newTestHandler(f, Config{}), tok, "", func(r *http.Request) {
			if ct != "" {
				r.Header.Set("Content-Type", ct)
			}
		})
		if rec.Code != http.StatusOK {
			t.Errorf("empty body with content type %q: %d", ct, rec.Code)
		}
	}
}

func TestCheckInCSRF(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"same origin header", map[string]string{"Origin": "http://waitlist.test"}, http.StatusOK},
		{"non-browser", nil, http.StatusOK},
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "http://evil.test"}, http.StatusForbidden},
	}
	for _, c := range cases {
		f := &fakeRegistrar{checkIn: diningResult(party.CheckInStarted)}
		tok, _ := token.Generate()
		rec := postCheckIn(newTestHandler(f, Config{}), tok, "", func(r *http.Request) {
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
		})
		if rec.Code != c.want {
			t.Errorf("%s: %d want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusForbidden && len(f.checkInCalls) != 0 {
			t.Errorf("%s: rejected request reached the store", c.name)
		}
	}
}

func TestCheckInOnlyPostIsRouted(t *testing.T) {
	f := &fakeRegistrar{}
	req := httptest.NewRequest(http.MethodGet, "http://waitlist.test/api/me/check-in", nil)
	rec := httptest.NewRecorder()
	newTestHandler(f, Config{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || len(f.checkInCalls) != 0 {
		t.Fatalf("GET check-in: %d", rec.Code)
	}
}
