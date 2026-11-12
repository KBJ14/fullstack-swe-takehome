// Package config는 환경변수에서 서버 설정을 읽는다.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Config는 서버 실행에 필요한 설정이다.
type Config struct {
	// HTTPAddr는 HTTP 서버가 수신할 주소다. (예: ":8080")
	HTTPAddr string
	// MongoURI는 MongoDB 접속 문자열이다. 로그나 응답에 출력하지 않는다.
	MongoURI string
	// MongoDatabase는 앱이 사용할 데이터베이스 이름이다.
	MongoDatabase string
	// ReadinessTimeout은 /readyz가 DB 확인에 쓰는 최대 시간이다.
	ReadinessTimeout time.Duration
}

// Load는 환경변수에서 설정을 읽고 검증한다.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		MongoURI:      os.Getenv("MONGODB_URI"),
		MongoDatabase: getenv("MONGODB_DATABASE", "waitlist"),
	}
	if cfg.MongoURI == "" {
		return Config{}, errors.New("MONGODB_URI is required")
	}

	timeout, err := time.ParseDuration(getenv("READINESS_TIMEOUT", "2s"))
	if err != nil || timeout <= 0 {
		return Config{}, fmt.Errorf("READINESS_TIMEOUT must be a positive duration")
	}
	cfg.ReadinessTimeout = timeout

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
