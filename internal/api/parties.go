package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"

	"github.com/KBJ14/fullstack-swe-takehome/internal/party"
	"github.com/KBJ14/fullstack-swe-takehome/internal/token"
)

// maxBodyBytes는 등록 요청 본문의 최대 크기다. 필드 세 개의 정상 입력보다 충분히 크다.
const maxBodyBytes = 4 << 10

type partiesHandler struct {
	registrar party.Registrar
	cfg       Config
	logger    *slog.Logger
}

// registerRequest는 요청 본문이다. 필드별 타입 오류를 구분하기 위해 원시 JSON으로 받는다.
type registerRequest struct {
	Name      json.RawMessage `json:"name"`
	Email     json.RawMessage `json:"email"`
	PartySize json.RawMessage `json:"partySize"`
}

type registerResponse struct {
	// Result는 "created"(새 등록) 또는 "existing"(쿠키가 가리키는 기존 진행 중 등록)이다.
	Result party.Outcome `json:"result"`
	Party  party.View    `json:"party"`
}

// create는 POST /api/parties 처리다.
//
// 처리 순서 (docs/design.md 2.7)
//
//  1. CSRF 검사 (라우터의 CrossOriginProtection) → 403
//  2. Content-Type이 application/json인지 확인 → 415
//  3. 본문 JSON 해석과 필드 검증 → 400
//     입력 검증은 DB를 보지 않는 순수 검사이므로, 기존 등록 여부와 관계없이 먼저 수행한다.
//  4. 쿠키 토큰과 함께 저장소에 등록 요청 (트랜잭션)
//     - 쿠키가 가리키는 진행 중 등록 → 200 (새 팀 없음, 쿠키 발급 없음)
//     - 진행 중인 같은 이메일 → 409 (기존 팀 정보 없음)
//     - 새 팀 생성 → 저장 성공 후에만 쿠키 발급 → 201
func (h *partiesHandler) create(w http.ResponseWriter, r *http.Request) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", msgUnsupportedMedia, nil)
		return
	}

	in, fields, ok := decodeRegistration(w, r)
	if !ok {
		if fields != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", msgInvalidInput, fields)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", msgInvalidJSON, nil)
		}
		return
	}

	newToken, err := token.Generate()
	if err != nil {
		h.internalError(w, err)
		return
	}

	res, err := h.registrar.Register(r.Context(), party.RegisterCommand{
		Input:              in,
		PresentedTokenHash: presentedTokenHash(r),
		NewTokenHash:       token.Hash(newToken),
	})
	switch {
	case errors.Is(err, party.ErrEmailInUse):
		writeError(w, http.StatusConflict, "email_in_use", msgEmailInUse, nil)
		return
	case err != nil:
		h.internalError(w, err)
		return
	}

	if res.Outcome == party.OutcomeCreated {
		// 트랜잭션이 커밋된 뒤에만 쿠키를 발급한다.
		http.SetCookie(w, h.newCookie(newToken))
		writeJSON(w, http.StatusCreated, registerResponse{Result: res.Outcome, Party: res.Party})
		return
	}
	writeJSON(w, http.StatusOK, registerResponse{Result: res.Outcome, Party: res.Party})
}

func (h *partiesHandler) internalError(w http.ResponseWriter, err error) {
	// 상세 원인은 로그에만 남긴다. 오류에는 토큰 원문이 들어가지 않는다.
	h.logger.Error("register party failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", msgInternal, nil)
}

// newCookie는 식별 쿠키를 만든다. 속성의 근거는 docs/design.md 2.8 참고.
func (h *partiesHandler) newCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(CookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// TokenHashFromRequest는 요청 쿠키의 토큰 해시를 돌려준다.
// 쿠키가 없거나 발급 형식이 아니면 빈 값을 돌려주어 쿠키가 없는 요청으로 처리한다.
// API와 화면(internal/web)이 같은 방식으로 쿠키를 해석하도록 공개한다.
func TokenHashFromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || !token.WellFormed(c.Value) {
		return ""
	}
	return token.Hash(c.Value)
}

func presentedTokenHash(r *http.Request) string { return TokenHashFromRequest(r) }

func isJSONContentType(v string) bool {
	mt, _, err := mime.ParseMediaType(v)
	return err == nil && mt == "application/json"
}

// decodeRegistration은 본문을 해석하고 검증한다.
//
// 반환값: 검증된 입력, 필드 오류(입력 오류일 때), 성공 여부.
// JSON 문법 오류·알 수 없는 필드·여러 JSON 값·크기 초과는 필드 오류 없이 실패한다.
func decodeRegistration(w http.ResponseWriter, r *http.Request) (party.Input, party.FieldErrors, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return party.Input{}, nil, false
	}

	var req registerRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return party.Input{}, nil, false
	}
	// 본문에 JSON 값이 하나만 있어야 한다.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return party.Input{}, nil, false
	}

	fields := party.FieldErrors{}
	var in party.Input

	if s, msg := jsonString(req.Name, party.MsgNameRequired, party.MsgNameType); msg != "" {
		fields["name"] = msg
	} else if name, msg := party.ValidateName(s); msg != "" {
		fields["name"] = msg
	} else {
		in.Name = name
	}

	if s, msg := jsonString(req.Email, party.MsgEmailRequired, party.MsgEmailType); msg != "" {
		fields["email"] = msg
	} else if trimmed, norm, msg := party.ValidateEmail(s); msg != "" {
		fields["email"] = msg
	} else {
		in.Email, in.EmailNormalized = trimmed, norm
	}

	if n, msg := jsonInt(req.PartySize); msg != "" {
		fields["partySize"] = msg
	} else if msg := party.ValidatePartySize(n); msg != "" {
		fields["partySize"] = msg
	} else {
		in.PartySize = n
	}

	if len(fields) > 0 {
		return party.Input{}, fields, false
	}
	return in, nil, true
}

func isAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// jsonString은 원시 JSON이 문자열이면 그 값을 돌려준다.
func jsonString(raw json.RawMessage, requiredMsg, typeMsg string) (string, string) {
	if isAbsent(raw) {
		return "", requiredMsg
	}
	var s string
	if raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", typeMsg
	}
	return s, ""
}

// integerLiteral은 소수점·지수 표기가 없는 JSON 정수 리터럴이다.
var integerLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// jsonInt는 원시 JSON이 정수 리터럴이면 그 값을 돌려준다.
// 문자열("2"), 소수(2.5, 2.0), 지수(1e1), 불리언 등은 모두 거부한다.
func jsonInt(raw json.RawMessage) (int, string) {
	// 빈 문자열은 값을 입력하지 않은 것으로 안내한다.
	if isAbsent(raw) || string(raw) == `""` {
		return 0, party.MsgPartySizeMissing
	}
	if !integerLiteral.Match(raw) {
		return 0, party.MsgPartySizeInvalid
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, party.MsgPartySizeInvalid
	}
	return n, ""
}
