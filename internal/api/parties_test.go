package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

// fakeRegistrar는 저장소 대신 호출 내용을 기록하고 정해 둔 결과를 돌려준다.
type fakeRegistrar struct {
	mu    sync.Mutex
	calls []party.RegisterCommand
	res   party.RegisterResult
	err   error

	statusCalls []string
	status      party.StatusView
	statusErr   error
}

func (f *fakeRegistrar) Register(_ context.Context, cmd party.RegisterCommand) (party.RegisterResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cmd)
	return f.res, f.err
}

func (f *fakeRegistrar) Status(_ context.Context, tokenHash string) (party.StatusView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls = append(f.statusCalls, tokenHash)
	return f.status, f.statusErr
}

func created() party.RegisterResult {
	return party.RegisterResult{Outcome: party.OutcomeCreated, Party: party.View{
		Name: "Kim", PartySize: 2, Status: party.StatusWaiting, RegisteredAt: time.Unix(0, 0).UTC(),
	}}
}

func newTestHandler(f *fakeRegistrar, cfg Config) http.Handler {
	return New(f, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func post(h http.Handler, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://waitlist.test/api/parties", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errResp struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errResp {
	t.Helper()
	var e errResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("invalid error body %q: %v", rec.Body.String(), err)
	}
	return e
}

const validBody = `{"name":"  Kim ","email":"  Kim@Example.COM ","partySize":2}`

func TestCreatedSetsCookieAndNormalizesInput(t *testing.T) {
	f := &fakeRegistrar{res: created()}
	rec := post(newTestHandler(f, Config{}), validBody, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(f.calls))
	}
	in := f.calls[0].Input
	if in.Name != "Kim" || in.Email != "Kim@Example.COM" || in.EmailNormalized != "kim@example.com" || in.PartySize != 2 {
		t.Fatalf("unexpected input %+v", in)
	}
	if f.calls[0].PresentedTokenHash != "" {
		t.Fatal("no cookie should mean no presented token")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" ||
		c.MaxAge != int(CookieMaxAge.Seconds()) || c.Secure {
		t.Fatalf("unexpected cookie attributes %+v", c)
	}
	// 저장소에는 쿠키 토큰의 해시만 전달된다.
	if !token.WellFormed(c.Value) || f.calls[0].NewTokenHash != token.Hash(c.Value) {
		t.Fatal("stored hash does not match issued cookie")
	}
	// 응답 본문에 토큰이 없다.
	if strings.Contains(rec.Body.String(), c.Value) || strings.Contains(rec.Body.String(), f.calls[0].NewTokenHash) {
		t.Fatal("token leaked into response body")
	}

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["result"] != "created" {
		t.Fatalf("unexpected body %v", body)
	}
	p := body["party"].(map[string]any)
	for _, k := range []string{"email", "sequence", "tokenHash", "aheadCount", "canCheckIn"} {
		if _, ok := p[k]; ok {
			t.Errorf("party view must not contain %q", k)
		}
	}
}

func TestSecureCookieWhenConfigured(t *testing.T) {
	f := &fakeRegistrar{res: created()}
	rec := post(newTestHandler(f, Config{CookieSecure: true}), validBody, nil)
	if c := rec.Result().Cookies(); len(c) != 1 || !c[0].Secure {
		t.Fatal("expected Secure cookie")
	}
}

func TestExistingReturns200WithoutCookie(t *testing.T) {
	res := created()
	res.Outcome = party.OutcomeExisting
	f := &fakeRegistrar{res: res}
	tok, _ := token.Generate()
	rec := post(newTestHandler(f, Config{}), validBody, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("existing registration must not issue a new cookie")
	}
	if f.calls[0].PresentedTokenHash != token.Hash(tok) {
		t.Fatal("presented token hash not passed")
	}
	if !strings.Contains(rec.Body.String(), `"result":"existing"`) {
		t.Fatalf("unexpected body %s", rec.Body)
	}
}

func TestMalformedCookieIsIgnored(t *testing.T) {
	f := &fakeRegistrar{res: created()}
	rec := post(newTestHandler(f, Config{}), validBody, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "garbage"})
	})
	if rec.Code != http.StatusCreated || f.calls[0].PresentedTokenHash != "" {
		t.Fatalf("malformed cookie should be ignored: %d %+v", rec.Code, f.calls[0])
	}
}

func TestEmailInUseReturns409WithoutPartyInfo(t *testing.T) {
	f := &fakeRegistrar{err: party.ErrEmailInUse}
	rec := post(newTestHandler(f, Config{}), validBody, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d", rec.Code)
	}
	e := decodeErr(t, rec)
	if e.Error.Code != "email_in_use" || e.Error.Message != "이 이메일로 진행 중인 등록이 있습니다. 등록했던 브라우저에서 확인해 주세요." {
		t.Fatalf("unexpected error %+v", e)
	}
	if strings.Contains(rec.Body.String(), "party") || len(rec.Result().Cookies()) != 0 {
		t.Fatal("409 must not include party info or cookie")
	}
}

func TestInternalErrorHidesDetails(t *testing.T) {
	f := &fakeRegistrar{err: errors.New("mongodb://secret-host:27017 exploded")}
	rec := post(newTestHandler(f, Config{}), validBody, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-host") || decodeErr(t, rec).Error.Code != "internal_error" {
		t.Fatalf("internal detail leaked: %s", rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no cookie on failure")
	}
}

func TestInvalidJSONBodies(t *testing.T) {
	bodies := map[string]string{
		"empty":         ``,
		"syntax":        `{"name":`,
		"array":         `[]`,
		"string":        `"x"`,
		"unknown field": `{"name":"Kim","email":"a@example.com","partySize":2,"admin":true}`,
		"two values":    `{"name":"Kim","email":"a@example.com","partySize":2}{}`,
		"too large":     `{"name":"` + strings.Repeat("a", maxBodyBytes) + `","email":"a@example.com","partySize":2}`,
	}
	for name, body := range bodies {
		f := &fakeRegistrar{res: created()}
		rec := post(newTestHandler(f, Config{}), body, nil)
		if rec.Code != http.StatusBadRequest || decodeErr(t, rec).Error.Code != "invalid_json" {
			t.Errorf("%s: status %d body %s", name, rec.Code, rec.Body)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: registrar must not be called", name)
		}
	}
}

func TestFieldValidation(t *testing.T) {
	cases := []struct {
		name, body, field, msg string
	}{
		{"name missing", `{"email":"a@example.com","partySize":2}`, "name", party.MsgNameRequired},
		{"name blank", `{"name":"   ","email":"a@example.com","partySize":2}`, "name", party.MsgNameRequired},
		{"name null", `{"name":null,"email":"a@example.com","partySize":2}`, "name", party.MsgNameRequired},
		{"name number", `{"name":123,"email":"a@example.com","partySize":2}`, "name", party.MsgNameType},
		{"email missing", `{"name":"Kim","partySize":2}`, "email", party.MsgEmailRequired},
		{"email blank", `{"name":"Kim","email":"  ","partySize":2}`, "email", party.MsgEmailRequired},
		{"email format", `{"name":"Kim","email":"abc","partySize":2}`, "email", party.MsgEmailInvalid},
		{"email format2", `{"name":"Kim","email":"a@","partySize":2}`, "email", party.MsgEmailInvalid},
		{"email type", `{"name":"Kim","email":["a@example.com"],"partySize":2}`, "email", party.MsgEmailType},
		{"size missing", `{"name":"Kim","email":"a@example.com"}`, "partySize", party.MsgPartySizeMissing},
		{"size null", `{"name":"Kim","email":"a@example.com","partySize":null}`, "partySize", party.MsgPartySizeMissing},
		{"size empty string", `{"name":"Kim","email":"a@example.com","partySize":""}`, "partySize", party.MsgPartySizeMissing},
		{"size zero", `{"name":"Kim","email":"a@example.com","partySize":0}`, "partySize", party.MsgPartySizeInvalid},
		{"size negative", `{"name":"Kim","email":"a@example.com","partySize":-1}`, "partySize", party.MsgPartySizeInvalid},
		{"size decimal", `{"name":"Kim","email":"a@example.com","partySize":2.5}`, "partySize", party.MsgPartySizeInvalid},
		{"size 2.0", `{"name":"Kim","email":"a@example.com","partySize":2.0}`, "partySize", party.MsgPartySizeInvalid},
		{"size exponent", `{"name":"Kim","email":"a@example.com","partySize":1e1}`, "partySize", party.MsgPartySizeInvalid},
		{"size eleven", `{"name":"Kim","email":"a@example.com","partySize":11}`, "partySize", party.MsgPartySizeInvalid},
		{"size huge", `{"name":"Kim","email":"a@example.com","partySize":99999999999999999999}`, "partySize", party.MsgPartySizeInvalid},
		{"size string", `{"name":"Kim","email":"a@example.com","partySize":"2"}`, "partySize", party.MsgPartySizeInvalid},
		{"size bool", `{"name":"Kim","email":"a@example.com","partySize":true}`, "partySize", party.MsgPartySizeInvalid},
	}
	for _, c := range cases {
		f := &fakeRegistrar{res: created()}
		rec := post(newTestHandler(f, Config{}), c.body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", c.name, rec.Code)
			continue
		}
		e := decodeErr(t, rec)
		if e.Error.Code != "invalid_input" || e.Error.Fields[c.field] != c.msg {
			t.Errorf("%s: unexpected error %+v", c.name, e)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: registrar must not be called", c.name)
		}
	}
}

func TestPartySizeBoundariesAccepted(t *testing.T) {
	for _, n := range []string{"1", "10"} {
		f := &fakeRegistrar{res: created()}
		rec := post(newTestHandler(f, Config{}), `{"name":"Kim","email":"a@example.com","partySize":`+n+`}`, nil)
		if rec.Code != http.StatusCreated {
			t.Errorf("partySize %s: status %d", n, rec.Code)
		}
	}
}

func TestContentTypeMustBeJSON(t *testing.T) {
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		f := &fakeRegistrar{res: created()}
		rec := post(newTestHandler(f, Config{}), validBody, func(r *http.Request) { r.Header.Set("Content-Type", ct) })
		if rec.Code != http.StatusUnsupportedMediaType || len(f.calls) != 0 {
			t.Errorf("content type %q: status %d", ct, rec.Code)
		}
	}
	f := &fakeRegistrar{res: created()}
	rec := post(newTestHandler(f, Config{}), validBody, func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json; charset=utf-8")
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("json with charset should be accepted: %d", rec.Code)
	}
}

func TestCSRF(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusCreated},
		{"user typed / none", map[string]string{"Sec-Fetch-Site": "none"}, http.StatusCreated},
		{"same origin header only", map[string]string{"Origin": "http://waitlist.test"}, http.StatusCreated},
		{"non-browser client (no headers)", nil, http.StatusCreated},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site other origin", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin header only", map[string]string{"Origin": "http://evil.test"}, http.StatusForbidden},
	}
	for _, c := range cases {
		f := &fakeRegistrar{res: created()}
		rec := post(newTestHandler(f, Config{}), validBody, func(r *http.Request) {
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
		})
		if rec.Code != c.want {
			t.Errorf("%s: status %d want %d", c.name, rec.Code, c.want)
			continue
		}
		if c.want == http.StatusForbidden {
			if len(f.calls) != 0 || len(rec.Result().Cookies()) != 0 {
				t.Errorf("%s: rejected request must not reach registrar or set cookie", c.name)
			}
			if decodeErr(t, rec).Error.Code != "forbidden" {
				t.Errorf("%s: unexpected body %s", c.name, rec.Body)
			}
		}
	}
}

func TestOnlyPostIsRouted(t *testing.T) {
	f := &fakeRegistrar{res: created()}
	req := httptest.NewRequest(http.MethodGet, "http://waitlist.test/api/parties", nil)
	rec := httptest.NewRecorder()
	newTestHandler(f, Config{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || len(f.calls) != 0 {
		t.Fatalf("GET status %d", rec.Code)
	}
}
