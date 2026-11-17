// Command server는 Remote Waitlist Manager HTTP 서버다.
//
// 현재 제공하는 경로: 상태 확인(/healthz, /readyz), 대기 등록(POST /api/parties).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KBJ14/fullstack-swe-takehome/internal/api"
	"github.com/KBJ14/fullstack-swe-takehome/internal/config"
	"github.com/KBJ14/fullstack-swe-takehome/internal/database"
	"github.com/KBJ14/fullstack-swe-takehome/internal/health"
	"github.com/KBJ14/fullstack-swe-takehome/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	client, err := database.Connect(cfg.MongoURI)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			logger.Warn("mongo disconnect failed", "error", err)
		}
	}()

	st := store.New(client, cfg.MongoDatabase)
	if err := ensureSchema(st, logger); err != nil {
		return err
	}

	mux := http.NewServeMux()
	health.Handler{
		Checker: database.Checker{Client: client},
		Timeout: cfg.ReadinessTimeout,
		Logger:  logger,
	}.Register(mux)
	mux.Handle("/api/", api.New(st, api.Config{CookieSecure: cfg.CookieSecure}, logger))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// ensureSchema는 요청을 받기 전에 인덱스와 초기 식당 문서를 준비한다.
// DB가 잠시 응답하지 않을 수 있으므로 제한 시간 안에서 재시도하고, 끝내 실패하면 서버를 시작하지 않는다.
func ensureSchema(st *store.Store, logger *slog.Logger) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := st.EnsureSchema(ctx)
		cancel()
		if err == nil {
			logger.Info("database schema ready")
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		logger.Warn("database schema not ready, retrying", "error", err)
		time.Sleep(time.Second)
	}
}
