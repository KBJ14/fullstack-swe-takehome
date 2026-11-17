// Package api는 JSON API(/api/...)를 제공한다.
//
// 상태를 바꾸는 요청(POST 등)은 모두 CSRF 보호를 거친다. (docs/design.md 2.8 참고)
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

// Config는 API 동작 설정이다.
type Config struct {
	// CookieSecure는 식별 쿠키에 Secure 속성을 붙일지 정한다.
	// HTTPS로 배포할 때는 true여야 한다. 로컬 HTTP(http://127.0.0.1)에서는
	// 브라우저가 Secure 쿠키를 저장하지 않을 수 있으므로 기본값은 false다.
	CookieSecure bool
}

// 식별 쿠키 설정.
const (
	// CookieName은 팀 식별 토큰을 담는 쿠키 이름이다.
	// "__Host-" 접두사는 Secure를 요구하므로 로컬 HTTP와 함께 쓸 수 없어 사용하지 않는다.
	CookieName = "waitlist_token"
	// CookieMaxAge는 쿠키 유효기간이다. 브라우저를 닫았다 다시 열어도 상태를 복원할 수 있도록
	// 세션 쿠키가 아닌 유지되는 쿠키로 발급한다. 대기·식사 시간보다 충분히 길면서,
	// 오래된 토큰이 무기한 남지 않도록 7일로 정한다.
	CookieMaxAge = 7 * 24 * time.Hour
)

// 공통 오류 안내 문구.
const (
	msgInvalidJSON      = "요청 형식이 올바르지 않습니다."
	msgInvalidInput     = "입력값을 확인해 주세요."
	msgUnsupportedMedia = "요청 형식은 application/json이어야 합니다."
	msgForbidden        = "허용되지 않은 요청입니다."
	msgEmailInUse       = "이 이메일로 진행 중인 등록이 있습니다. 등록했던 브라우저에서 확인해 주세요."
	msgNotRegistered    = "등록 정보를 확인할 수 없습니다."
	msgInternal         = "일시적인 오류가 발생했습니다. 잠시 후 다시 시도해 주세요."
)

// Service는 API가 사용하는 저장소 기능이다.
type Service interface {
	party.Registrar
	party.StatusReader
}

// New는 API 핸들러를 만든다.
func New(svc Service, cfg Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	p := &partiesHandler{registrar: svc, cfg: cfg, logger: logger}
	mux.HandleFunc("POST /api/parties", p.create)
	m := &meHandler{reader: svc, logger: logger}
	mux.HandleFunc("GET /api/me", m.get)

	// CSRF 대응: Go 표준 라이브러리의 CrossOriginProtection.
	//   - 브라우저가 보내는 Sec-Fetch-Site가 same-origin/none이 아니면 거부
	//   - Sec-Fetch-Site가 없으면 Origin 헤더의 호스트가 요청 Host와 같은지 비교
	//   - 두 헤더가 모두 없으면 브라우저가 아닌 요청(curl 등)으로 보고 허용
	// GET/HEAD/OPTIONS는 검사하지 않으므로, 상태 변경은 이 메서드들로 하지 않는다.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "forbidden", msgForbidden, nil)
	}))
	return cop.Handler(mux)
}

// errorBody는 모든 오류 응답의 공통 형식이다.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func writeError(w http.ResponseWriter, code int, errCode, msg string, fields map[string]string) {
	writeJSON(w, code, errorBody{Error: errorDetail{Code: errCode, Message: msg, Fields: fields}})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
