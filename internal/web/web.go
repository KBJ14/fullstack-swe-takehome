// Package web은 손님용 화면(SSR + 순수 JavaScript Islands)을 제공한다.
//
//   - 페이지는 Go html/template으로 실제 내용이 있는 HTML을 서버에서 만든다.
//   - JavaScript는 두 Island만 담당한다: 등록 폼(static/join.js), 내 상태 카드(static/status.js).
//   - 페이지 이동은 일반 링크·브라우저 탐색이다. 클라이언트 라우터는 없다.
//   - 초기 상태는 API를 HTTP로 다시 부르지 않고, API와 같은 저장소 코드(party.StatusReader)로 조회한다.
//
// 화면 경로: GET /(안내), GET /join(등록), GET /status(내 상태), GET /static/...(CSS·JS)
package web

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/KBJ14/fullstack-swe-takehome/internal/api"
	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var pages = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Handler는 화면 경로를 처리한다.
type Handler struct {
	Reader party.StatusReader
	Logger *slog.Logger
}

// Register는 mux에 화면 경로를 등록한다. 기존 /api/, /healthz, /readyz와 겹치지 않는다.
func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.root)
	mux.HandleFunc("GET /join", h.join)
	mux.HandleFunc("GET /status", h.status)

	sub, _ := fs.Sub(staticFS, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 디렉터리 목록은 보여 주지 않는다.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
}

// lookup은 쿠키가 가리키는 팀의 상태를 조회한다.
// 미등록이면 (nil, nil), DB 오류·끊어진 참조 등은 오류로 돌려준다. (미등록으로 숨기지 않음)
func (h Handler) lookup(r *http.Request) (*party.StatusView, error) {
	hash := api.TokenHashFromRequest(r)
	if hash == "" {
		return nil, nil
	}
	v, err := h.Reader.Status(r.Context(), hash)
	if errors.Is(err, party.ErrNotRegistered) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// root는 GET /이다. 등록이 있으면 내 상태 화면, 없으면 등록 화면으로 보낸다.
func (h Handler) root(w http.ResponseWriter, r *http.Request) {
	v, err := h.lookup(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	target := "/join"
	if v != nil {
		target = "/status"
	}
	noStore(w)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// join은 GET /join이다. 진행 중 등록이 있으면 내 상태 화면으로 보낸다.
// 완료된 팀이거나 등록이 없으면 새 등록 폼을 보여 준다.
func (h Handler) join(w http.ResponseWriter, r *http.Request) {
	v, err := h.lookup(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if v != nil && v.Party.Status.Active() {
		noStore(w)
		http.Redirect(w, r, "/status", http.StatusSeeOther)
		return
	}
	h.render(w, http.StatusOK, "join.html", nil)
}

// status는 GET /status다. 상태를 바꾸지 않고, 쿠키도 발급하지 않는다.
func (h Handler) status(w http.ResponseWriter, r *http.Request) {
	v, err := h.lookup(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, http.StatusOK, "status.html", newStatusPage(v))
}

func (h Handler) serverError(w http.ResponseWriter, err error) {
	// 상세 원인은 로그에만 남긴다.
	h.Logger.Error("page lookup failed", "error", err)
	h.render(w, http.StatusInternalServerError, "error.html", nil)
}

func (h Handler) render(w http.ResponseWriter, code int, name string, data any) {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, data); err != nil {
		h.Logger.Error("render page failed", "page", name, "error", err)
		noStore(w)
		http.Error(w, "일시적인 오류가 발생했습니다.", http.StatusInternalServerError)
		return
	}
	noStore(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; "+
			"form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

// noStore는 개인 상태가 담긴 응답을 캐시하지 않게 한다.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
