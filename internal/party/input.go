package party

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

// 입력 제한값.
const (
	MinPartySize = 1
	// MaxPartySize는 식당 정원(10석)과 같다. 정원보다 큰 팀은 입장할 수 없다.
	MaxPartySize = 10
	// MaxNameLength는 이름의 최대 글자 수(rune 기준)다. 화면 표시와 저장 크기를 제한하기 위한 최소 제한이다.
	MaxNameLength = 100
	// MaxEmailLength는 이메일 주소의 실용적 최대 길이(RFC 5321의 경로 길이 제한 기준)다.
	MaxEmailLength = 254
)

// 필드별 오류 안내 문구.
const (
	MsgNameRequired     = "이름을 입력해 주세요."
	MsgNameTooLong      = "이름은 100자 이하로 입력해 주세요."
	MsgNameType         = "이름은 문자열이어야 합니다."
	MsgEmailRequired    = "이메일을 입력해 주세요."
	MsgEmailInvalid     = "올바른 이메일 형식이 아닙니다."
	MsgEmailTooLong     = "이메일은 254자 이하로 입력해 주세요."
	MsgEmailType        = "이메일은 문자열이어야 합니다."
	MsgPartySizeMissing = "인원수를 입력해 주세요."
	MsgPartySizeInvalid = "인원수는 1~10 사이의 정수로 입력해 주세요."
)

// Input은 검증을 통과한 등록 입력이다.
type Input struct {
	// Name은 앞뒤 공백을 제거한 이름이다.
	Name string
	// Email은 앞뒤 공백을 제거한 입력 이메일이다.
	Email string
	// EmailNormalized는 중복 비교에 쓰는 정규화 이메일이다. (공백 제거 + 소문자)
	EmailNormalized string
	PartySize       int
}

// FieldErrors는 필드 이름별 오류 안내다. 키는 JSON 필드 이름과 같다.
type FieldErrors map[string]string

// NormalizeEmail은 이메일의 앞뒤 공백을 제거하고 소문자로 통일한다.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateName은 이름을 검증하고 앞뒤 공백을 제거한 값을 돌려준다.
func ValidateName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", MsgNameRequired
	}
	if utf8.RuneCountInString(name) > MaxNameLength {
		return "", MsgNameTooLong
	}
	return name, ""
}

// ValidateEmail은 이메일 형식을 검증하고, 공백을 제거한 값과 정규화 값을 돌려준다.
//
// 형식 기준: net/mail로 해석되는 단일 주소이면서
// 표시 이름이나 꺾쇠괄호 없이 주소만 있고, 도메인에 점(.)이 있어야 한다.
func ValidateEmail(raw string) (trimmed, normalized, msg string) {
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", MsgEmailRequired
	}
	if len(trimmed) > MaxEmailLength {
		return "", "", MsgEmailTooLong
	}
	addr, err := mail.ParseAddress(trimmed)
	if err != nil || addr.Name != "" || addr.Address != trimmed {
		return "", "", MsgEmailInvalid
	}
	at := strings.LastIndex(trimmed, "@")
	domain := trimmed[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", "", MsgEmailInvalid
	}
	return trimmed, NormalizeEmail(trimmed), ""
}

// ValidatePartySize는 인원수가 1~10의 정수인지 확인한다.
func ValidatePartySize(n int) string {
	if n < MinPartySize || n > MaxPartySize {
		return MsgPartySizeInvalid
	}
	return ""
}
