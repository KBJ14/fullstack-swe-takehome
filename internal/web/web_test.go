package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/api"
	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

type fakeReader struct {
	view  party.StatusView
	err   error
	calls int
}

func (f *fakeReader) Status(_ context.Context, _ string) (party.StatusView, error) {
	f.calls++
	return f.view, f.err
}

var at = time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)

func waitingView(ahead int64, can bool) party.StatusView {
	return party.StatusView{
		Party:   party.View{Name: "Kim", PartySize: 2, Status: party.StatusWaiting, RegisteredAt: at},
		Waiting: &party.WaitingInfo{AheadCount: ahead, CanCheckIn: can},
	}
}

func serve(f *fakeReader, path string, withCookie bool) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	Handler{Reader: f, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if withCookie {
		tok, _ := token.Generate()
		req.AddCookie(&http.Cookie{Name: api.CookieName, Value: tok})
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// visible은 hidden 속성이 없는 data-section을 돌려준다.
func visibleSections(body string) []string {
	var out []string
	for _, part := range strings.Split(body, `data-section="`)[1:] {
		name := part[:strings.Index(part, `"`)]
		rest := part[len(name)+1:]
		if !strings.HasPrefix(strings.TrimSpace(rest), "hidden") {
			out = append(out, name)
		}
	}
	return out
}

func TestStatusPageRendersEachPhase(t *testing.T) {
	cases := []struct {
		name  string
		view  party.StatusView
		phase string
		want  []string
	}{
		{"waiting", waitingView(2, false), "waiting", []string{"내 앞에 2팀", "앞 팀이 입장하면 순서가 줄어듭니다.", "Kim · 2명"}},
		{"head without seats", waitingView(0, false), "waiting", []string{"다음 순서입니다", "빈 좌석이 생기면 체크인 버튼이 나타납니다."}},
		{"ready", waitingView(0, true), "ready", []string{"입장 차례입니다", `id="check-in"`}},
		{"dining", party.StatusView{
			Party:  party.View{Name: "Kim", PartySize: 2, Status: party.StatusDining, RegisteredAt: at},
			Dining: &party.DiningInfo{ServiceEndsAt: at.Add(5500 * time.Millisecond), ServerTime: at},
		}, "dining", []string{"식사 중입니다", "6초"}},
		{"completed", party.StatusView{
			Party: party.View{Name: "Kim", PartySize: 2, Status: party.StatusCompleted, RegisteredAt: at},
		}, "completed", []string{"이용이 완료되었습니다", `href="/join"`}},
	}
	for _, c := range cases {
		rec := serve(&fakeReader{view: c.view}, "/status", true)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", c.name, rec.Code)
		}
		if !strings.Contains(body, `data-phase="`+c.phase+`"`) {
			t.Errorf("%s: wrong phase", c.name)
		}
		if got := visibleSections(body); len(got) != 1 || got[0] != c.phase {
			t.Errorf("%s: visible sections %v", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", c.name, w)
			}
		}
		// 초기 상태 JSON이 같은 형식(GET /api/me)으로 들어 있다.
		if !strings.Contains(body, `id="initial-state">{"party":{"name":"Kim"`) {
			t.Errorf("%s: initial state missing", c.name)
		}
	}
}

func TestStatusPageWithoutRegistration(t *testing.T) {
	for name, tc := range map[string]struct {
		cookie bool
		err    error
	}{"no cookie": {false, nil}, "unknown token": {true, party.ErrNotRegistered}} {
		f := &fakeReader{err: tc.err}
		rec := serve(f, "/status", tc.cookie)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "등록 정보를 확인할 수 없습니다") ||
			!strings.Contains(body, `data-phase="unregistered"`) || !strings.Contains(body, `id="initial-state"> null <`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if !tc.cookie && f.calls != 0 {
			t.Errorf("%s: store queried without cookie", name)
		}
	}
}

func TestDatabaseErrorIsNotTreatedAsUnregistered(t *testing.T) {
	for _, path := range []string{"/", "/join", "/status"} {
		rec := serve(&fakeReader{err: errors.New("successorId points to a missing party: party 65abc")}, path, true)
		body := rec.Body.String()
		if rec.Code != http.StatusInternalServerError || !strings.Contains(body, "잠시 후 다시 시도해 주세요") {
			t.Errorf("%s: %d", path, rec.Code)
		}
		if strings.Contains(body, "등록 정보를 확인할 수 없습니다") || strings.Contains(body, "join-form") ||
			strings.Contains(body, "65abc") || strings.Contains(body, "successor") {
			t.Errorf("%s: error page hides the error or leaks details", path)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cache-control", path)
		}
	}
}

func TestRootAndJoinRouting(t *testing.T) {
	completed := party.StatusView{Party: party.View{Name: "Kim", PartySize: 2, Status: party.StatusCompleted, RegisteredAt: at}}
	cases := []struct {
		name, path string
		cookie     bool
		view       party.StatusView
		err        error
		code       int
		location   string
	}{
		{"root without cookie", "/", false, party.StatusView{}, nil, http.StatusSeeOther, "/join"},
		{"root unknown token", "/", true, party.StatusView{}, party.ErrNotRegistered, http.StatusSeeOther, "/join"},
		{"root active", "/", true, waitingView(1, false), nil, http.StatusSeeOther, "/status"},
		{"root completed", "/", true, completed, nil, http.StatusSeeOther, "/status"},
		{"join active", "/join", true, waitingView(1, false), nil, http.StatusSeeOther, "/status"},
		{"join completed shows form", "/join", true, completed, nil, http.StatusOK, ""},
		{"join without cookie", "/join", false, party.StatusView{}, nil, http.StatusOK, ""},
	}
	for _, c := range cases {
		rec := serve(&fakeReader{view: c.view, err: c.err}, c.path, c.cookie)
		if rec.Code != c.code || rec.Header().Get("Location") != c.location {
			t.Errorf("%s: %d %q", c.name, rec.Code, rec.Header().Get("Location"))
		}
		if c.code == http.StatusOK && !strings.Contains(rec.Body.String(), `id="join-form"`) {
			t.Errorf("%s: join form missing", c.name)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: headers", c.name)
		}
	}
}

func TestJoinFormMarkup(t *testing.T) {
	body := serve(&fakeReader{}, "/join", false).Body.String()
	for _, w := range []string{
		`<label for="name">이름</label>`, `<label for="email">이메일</label>`, `<label for="partySize">인원수</label>`,
		`min="1" max="10" step="1"`, `aria-describedby="name-error"`, `role="alert"`,
		`<noscript>`, `대기 등록에는 JavaScript가 필요합니다`, `<script src="/static/join.js" defer>`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("join page missing %q", w)
		}
	}
	// JavaScript가 없으면 제출할 수 없도록 버튼은 처음에 비활성화 상태다.
	if !strings.Contains(body, `<button type="submit" class="btn primary" disabled>`) {
		t.Error("submit button must start disabled until the island runs")
	}
}

func TestUserInputIsEscaped(t *testing.T) {
	v := waitingView(1, false)
	v.Party.Name = `<img src=x onerror=alert(1)></script><script>alert(2)</script>`
	body := serve(&fakeReader{view: v}, "/status", true).Body.String()
	if strings.Contains(body, "<img src=x") || strings.Contains(body, "<script>alert(2)") {
		t.Fatalf("unescaped user input:\n%s", body)
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatal("escaped name missing in HTML")
	}
	// JSON 초기 상태 안에서도 </script>로 빠져나갈 수 없다.
	if strings.Count(body, "</script>") != 2 {
		t.Fatalf("unexpected </script> count: %d", strings.Count(body, "</script>"))
	}
}

func TestSecurityAndCacheHeaders(t *testing.T) {
	rec := serve(&fakeReader{view: waitingView(0, true)}, "/status", true)
	h := rec.Header()
	if h.Get("Cache-Control") != "no-store" || !strings.HasPrefix(h.Get("Content-Type"), "text/html") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("pages must not set cookies")
	}
}

func TestStaticFiles(t *testing.T) {
	for path, ct := range map[string]string{
		"/static/app.css": "text/css", "/static/join.js": "text/javascript", "/static/status.js": "text/javascript",
	} {
		rec := serve(&fakeReader{}, path, false)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if rec := serve(&fakeReader{}, "/static/", false); rec.Code != http.StatusNotFound {
		t.Errorf("directory listing: %d", rec.Code)
	}
	if rec := serve(&fakeReader{}, "/static/missing.js", false); rec.Code != http.StatusNotFound {
		t.Errorf("missing file: %d", rec.Code)
	}
}

func TestDisplayRules(t *testing.T) {
	cases := map[time.Duration]string{
		0: "이용 완료 확인 중", -time.Second: "이용 완료 확인 중", time.Millisecond: "1초",
		1000 * time.Millisecond: "1초", 1001 * time.Millisecond: "2초", 30 * time.Second: "30초",
		61 * time.Second: "1분 01초", 125 * time.Second: "2분 05초",
	}
	for d, want := range cases {
		if got := remainingText(d); got != want {
			t.Errorf("remainingText(%v) = %q want %q", d, got, want)
		}
	}
	if aheadText(3) != "내 앞에 3팀" || aheadText(0) != "다음 순서입니다" {
		t.Error("aheadText")
	}
	if phaseOf(nil) != "unregistered" {
		t.Error("phaseOf(nil)")
	}
}
