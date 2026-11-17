// Package token은 팀 식별 토큰의 발급과 검증용 해시를 담당한다.
//
// 토큰 원문은 쿠키에만 담고, DB에는 Hash 결과만 저장한다.
// 토큰은 32바이트 암호학적 무작위 값이라 추측이 불가능하므로,
// 비밀번호용 느린 해시가 아닌 SHA-256으로 충분하다.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// byteLength는 무작위 토큰의 바이트 수(256비트)다.
const byteLength = 32

// encodedLength는 base64url(패딩 없음)로 인코딩한 토큰 길이다.
var encodedLength = base64.RawURLEncoding.EncodedLen(byteLength)

// Generate는 새 무작위 토큰을 만든다.
func Generate() (string, error) {
	b := make([]byte, byteLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// WellFormed는 값이 이 패키지가 발급하는 토큰 형식인지 확인한다.
// 형식이 맞지 않는 쿠키 값은 DB를 조회하지 않고 무시하기 위해 쓴다.
func WellFormed(t string) bool {
	if len(t) != encodedLength {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil && len(b) == byteLength
}

// Hash는 DB에 저장하고 조회할 토큰 검증값(SHA-256 16진수)을 만든다.
func Hash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
