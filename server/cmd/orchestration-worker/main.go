package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/orchestration"
)

func main() {
	logger.Init()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	cfg := orchestration.WorkerConfig{
		HostPort:  os.Getenv("TEMPORAL_HOST_PORT"),
		Namespace: os.Getenv("TEMPORAL_NAMESPACE"),
		TaskQueue: os.Getenv("TEMPORAL_TASK_QUEUE"),
		RedisURL:  os.Getenv("REDIS_URL"),
		Eino: orchestration.EinoReasoningConfig{
			Provider:            os.Getenv("ORCHESTRATION_EINO_PROVIDER"),
			APIKey:              os.Getenv("ORCHESTRATION_EINO_API_KEY"),
			Model:               os.Getenv("ORCHESTRATION_EINO_MODEL"),
			BaseURL:             os.Getenv("ORCHESTRATION_EINO_BASE_URL"),
			Timeout:             envDuration("ORCHESTRATION_EINO_TIMEOUT", 60*time.Second),
			AllowStatic:         os.Getenv("ORCHESTRATION_EINO_ALLOW_STATIC") == "1",
			ReasoningProfileRef: envString("ORCHESTRATION_EINO_PROFILE_REF", orchestration.DefaultReasoningProfileRef),
		},
	}
	if err := orchestration.Run(ctx, pool, cfg); err != nil {
		slog.Error("orchestration worker stopped with error", "error", err)
		os.Exit(1)
	}
}

func envString(name, def string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	return raw
}

func envDuration(name string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid duration env var, using default", "name", name, "value", raw, "default", def.String(), "error", err)
		return def
	}
	return d
}
