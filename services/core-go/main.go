package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"luma/core/internal/ai"
	"luma/core/internal/db"
	"luma/core/internal/focus"
	"luma/core/internal/httpapi"
	"luma/core/internal/memory"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	port := getenv("CORE_PORT", "52123")
	aiURL := getenv("AI_URL", "http://127.0.0.1:8788")
	dbPath := getenv("DB_PATH", "./data/luma.db")

	store, err := db.Open(dbPath)
	if err != nil {
		logger.Error("db init failed", slog.Any("error", err))
		os.Exit(1)
	}

	aiClient := ai.NewClient(aiURL)
	focusMonitor := focus.NewMonitor(store, logger, focusInterval())
	focusMonitor.Start()

	memoryService := memory.NewService(store.DB(), logger)
	handler := httpapi.NewHandler(store, aiClient, focusMonitor, memoryService, logger)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler.Router(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	logger.Info("core service listening", slog.String("addr", server.Addr))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server crashed", slog.Any("error", err))
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func focusInterval() time.Duration {
	if raw := os.Getenv("FOCUS_POLL_MS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return time.Duration(parsed) * time.Millisecond
		}
	}
	return time.Second
}
