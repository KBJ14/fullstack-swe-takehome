package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
)

// 체크인 안내 문구.
const (
	msgUnexpectedBody   = "체크인 요청에는 본문을 보내지 않습니다."
	msgNotYourTurn      = "아직 입장 순서가 아닙니다. 앞 팀이 먼저 입장해야 합니다."
	msgNotEnoughSeats   = "아직 빈 좌석이 부족합니다. 잠시 후 다시 확인해 주세요."
	msgAlreadyCompleted = "이미 이용을 마친 등록입니다. 다시 이용하려면 새로 등록해 주세요."
)

type checkInHandler struct {
	checker party.CheckInner
	logger  *slog.Logger
}

// checkInResponse는 GET /api/me와 같은 형식에 결과 종류(result)를 더한 것이다.
type checkInResponse struct {
	Result party.CheckInOutcome `json:"result"`
	party.StatusView
}

// post는 POST /api/me/check-in 처리다.
//
// 처리 순서 (docs/design.md 2.7)
//
//  1. CSRF 검사 (라우터의 CrossOriginProtection) → 403
//  2. 요청 본문은 비어 있어야 한다 → 아니면 400. Content-Type은 보지 않는다.
//     클라이언트는 팀 ID·인원·시각을 지정할 수 없고, canCheckIn 값도 보내지 않는다.
//  3. 쿠키 없음·형식 오류 → 401
//  4. 체크인 트랜잭션 → 200(checked_in / already_dining), 401, 409, 500
//
// 쿠키를 새로 발급하거나 회전하지 않는다. 응답은 Cache-Control: no-store.
func (h *checkInHandler) post(w http.ResponseWriter, r *http.Request) {
	// 본문이 1바이트라도 있으면 거부한다. (큰 본문을 끝까지 읽지 않도록 1바이트만 확인)
	if n, _ := io.ReadFull(io.LimitReader(r.Body, 1), make([]byte, 1)); n > 0 {
		writeError(w, http.StatusBadRequest, "unexpected_body", msgUnexpectedBody, nil)
		return
	}

	hash := presentedTokenHash(r)
	if hash == "" {
		writeError(w, http.StatusUnauthorized, "not_registered", msgNotRegistered, nil)
		return
	}

	res, err := h.checker.CheckIn(r.Context(), hash)
	switch {
	case errors.Is(err, party.ErrNotRegistered):
		writeError(w, http.StatusUnauthorized, "not_registered", msgNotRegistered, nil)
	case errors.Is(err, party.ErrNotYourTurn):
		writeError(w, http.StatusConflict, "not_your_turn", msgNotYourTurn, nil)
	case errors.Is(err, party.ErrNotEnoughSeats):
		writeError(w, http.StatusConflict, "not_enough_seats", msgNotEnoughSeats, nil)
	case errors.Is(err, party.ErrAlreadyCompleted):
		writeError(w, http.StatusConflict, "already_completed", msgAlreadyCompleted, nil)
	case err != nil:
		h.logger.Error("check in failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", msgInternal, nil)
	default:
		writeJSON(w, http.StatusOK, checkInResponse{Result: res.Outcome, StatusView: res.View})
	}
}
