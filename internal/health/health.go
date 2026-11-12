// Package health는 서버 생존(/healthz)과 준비(/readyz) 확인 경로를 제공한다.
//
// 외부 응답에는 상태 값만 담고, 접속 정보나 내부 오류 상세는 서버 로그에만 남긴다.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ReadinessChecker는 의존 서비스(DB)의 준비 여부를 확인한다.
type ReadinessChecker interface {
	CheckReady(ctx context.Context) error
}

// Handler는 상태 확인 HTTP 핸들러다.
type Handler struct {
	Checker ReadinessChecker
	// Timeout은 준비 확인 한 번에 허용하는 최대 시간이다.
	Timeout time.Duration
	Logger  *slog.Logger
}

// Register는 mux에 /healthz와 /readyz를 등록한다.
func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
}

// healthz는 프로세스가 요청을 처리할 수 있는지만 확인한다. DB는 확인하지 않는다.
func (h Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// readyz는 제한 시간 안에 DB 연결과 replica set primary를 확인한다.
func (h Handler) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()

	if err := h.Checker.CheckReady(ctx); err != nil {
		// 상세 원인은 로그에만 남긴다.
		h.Logger.Warn("readiness check failed", "error", err)
		writeStatus(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	writeStatus(w, http.StatusOK, "ready")
}

func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
