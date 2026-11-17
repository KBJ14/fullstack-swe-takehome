package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

func getMe(h http.Handler, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://waitlist.test/api/me", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMeUnauthorizedWithoutValidCookie(t *testing.T) {
	for name, cookie := range map[string]string{"no cookie": "", "malformed": "garbage"} {
		f := &fakeRegistrar{}
		rec := getMe(newTestHandler(f, Config{}), cookie)
		e := decodeErr(t, rec)
		if rec.Code != http.StatusUnauthorized || e.Error.Code != "not_registered" || e.Error.Message != "등록 정보를 확인할 수 없습니다." {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if len(f.statusCalls) != 0 {
			t.Errorf("%s: store must not be queried", name)
		}
	}
}

func TestMeUnknownTokenIs401(t *testing.T) {
	f := &fakeRegistrar{statusErr: party.ErrNotRegistered}
	tok, _ := token.Generate()
	rec := getMe(newTestHandler(f, Config{}), tok)
	if rec.Code != http.StatusUnauthorized || decodeErr(t, rec).Error.Code != "not_registered" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(f.statusCalls) != 1 || f.statusCalls[0] != token.Hash(tok) {
		t.Fatal("store must receive only the token hash")
	}
}

func TestMeInternalErrorHidesDetails(t *testing.T) {
	f := &fakeRegistrar{statusErr: errors.New("successorId points to a missing party: party 65abc")}
	tok, _ := token.Generate()
	rec := getMe(newTestHandler(f, Config{}), tok)
	if rec.Code != http.StatusInternalServerError || decodeErr(t, rec).Error.Code != "internal_error" ||
		strings.Contains(rec.Body.String(), "successor") || strings.Contains(rec.Body.String(), "65abc") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestMeResponseShapeAndHeaders(t *testing.T) {
	at := time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)
	cases := map[string]struct {
		view          party.StatusView
		want, missing []string
	}{
		"waiting": {
			party.StatusView{Party: party.View{Name: "Kim", PartySize: 2, Status: party.StatusWaiting, RegisteredAt: at},
				Waiting: &party.WaitingInfo{AheadCount: 3, CanCheckIn: false}},
			[]string{`"waiting":{"aheadCount":3,"canCheckIn":false}`, `"status":"waiting"`},
			[]string{`"dining"`},
		},
		"dining": {
			party.StatusView{Party: party.View{Name: "Kim", PartySize: 2, Status: party.StatusDining, RegisteredAt: at},
				Dining: &party.DiningInfo{ServiceEndsAt: at.Add(6 * time.Second), ServerTime: at}},
			[]string{`"dining":{"serviceEndsAt":"2026-11-17T01:02:09Z","serverTime":"2026-11-17T01:02:03Z"}`},
			[]string{`"waiting"`, `aheadCount`, `canCheckIn`},
		},
		"completed": {
			party.StatusView{Party: party.View{Name: "Kim", PartySize: 2, Status: party.StatusCompleted, RegisteredAt: at}},
			[]string{`"status":"completed"`},
			[]string{`"waiting"`, `"dining"`},
		},
	}
	for name, c := range cases {
		f := &fakeRegistrar{status: c.view}
		tok, _ := token.Generate()
		rec := getMe(newTestHandler(f, Config{}), tok)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", name, rec.Code)
			continue
		}
		body := rec.Body.String()
		for _, s := range c.want {
			if !strings.Contains(body, s) {
				t.Errorf("%s: body %s missing %s", name, body, s)
			}
		}
		for _, s := range c.missing {
			if strings.Contains(body, s) {
				t.Errorf("%s: body %s must not contain %s", name, body, s)
			}
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", name, rec.Header().Get("Cache-Control"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: GET must not set cookies", name)
		}
		if strings.Contains(body, tok) || strings.Contains(body, token.Hash(tok)) {
			t.Errorf("%s: token leaked", name)
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		for k := range m {
			if k != "party" && k != "waiting" && k != "dining" {
				t.Errorf("%s: unexpected top-level field %q", name, k)
			}
		}
	}
}

func TestMeOnlyGetIsRouted(t *testing.T) {
	f := &fakeRegistrar{}
	req := httptest.NewRequest(http.MethodPost, "http://waitlist.test/api/me", nil)
	rec := httptest.NewRecorder()
	newTestHandler(f, Config{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || len(f.statusCalls) != 0 {
		t.Fatalf("POST /api/me: %d", rec.Code)
	}
}
