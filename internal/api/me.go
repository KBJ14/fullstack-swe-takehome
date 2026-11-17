package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

type meHandler struct {
	reader party.StatusReader
	logger *slog.Logger
}

// get은 GET /api/me 처리다. 식별 쿠키로 내 팀의 현재 상태를 돌려준다.
//
//   - 쿠키 없음, 형식 오류, 연결된 팀 없음 → 401 (어떤 팀 정보도 없음)
//   - 팀을 찾으면 완료 상태도 200
//   - 아무것도 바꾸지 않고, 쿠키를 새로 발급하거나 회전하지 않는다.
//   - 응답은 개인 상태이므로 Cache-Control: no-store (writeJSON이 설정)
func (h *meHandler) get(w http.ResponseWriter, r *http.Request) {
	hash := presentedTokenHash(r)
	if hash == "" {
		writeError(w, http.StatusUnauthorized, "not_registered", msgNotRegistered, nil)
		return
	}

	view, err := h.reader.Status(r.Context(), hash)
	switch {
	case errors.Is(err, party.ErrNotRegistered):
		writeError(w, http.StatusUnauthorized, "not_registered", msgNotRegistered, nil)
		return
	case err != nil:
		h.logger.Error("read party status failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", msgInternal, nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
