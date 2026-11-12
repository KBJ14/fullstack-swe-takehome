package health

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
)

type fakeChecker struct {
	err   error
	delay time.Duration
}

func (f fakeChecker) CheckReady(ctx context.Context) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func newServer(c ReadinessChecker, timeout time.Duration) *httptest.Server {
	mux := http.NewServeMux()
	Handler{
		Checker: c,
		Timeout: timeout,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}.Register(mux)
	return httptest.NewServer(mux)
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(body))
}

func TestHealthzDoesNotDependOnDB(t *testing.T) {
	srv := newServer(fakeChecker{err: errors.New("db down")}, time.Second)
	defer srv.Close()

	code, body := get(t, srv.URL+"/healthz")
	if code != http.StatusOK || body != `{"status":"ok"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestReadyzReady(t *testing.T) {
	srv := newServer(fakeChecker{}, time.Second)
	defer srv.Close()

	code, body := get(t, srv.URL+"/readyz")
	if code != http.StatusOK || body != `{"status":"ready"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestReadyzHidesErrorDetails(t *testing.T) {
	secret := "mongodb://user:pass@mongo:27017 connection refused"
	srv := newServer(fakeChecker{err: errors.New(secret)}, time.Second)
	defer srv.Close()

	code, body := get(t, srv.URL+"/readyz")
	if code != http.StatusServiceUnavailable || body != `{"status":"not_ready"}` {
		t.Fatalf("got %d %s", code, body)
	}
}

func TestReadyzTimesOut(t *testing.T) {
	srv := newServer(fakeChecker{delay: 5 * time.Second}, 50*time.Millisecond)
	defer srv.Close()

	start := time.Now()
	code, _ := get(t, srv.URL+"/readyz")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", code)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("readyz did not respect timeout: %v", elapsed)
	}
}
