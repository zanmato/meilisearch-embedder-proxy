package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/zanmato/meilisearch-embedder-proxy/internal/cache"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/config"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/database"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/hash"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/logger"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/openai"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/server"
	"github.com/zanmato/meilisearch-embedder-proxy/internal/tracker"
)

var (
	configPath = flag.String("config", "config.toml", "Path to configuration file")
	version    = flag.Bool("version", false, "Show version information")
)

const (
	AppName    = "Meep"
	AppVersion = "1.0.0"
	AppDesc    = "Meilisearch Embedder Proxy"
)

func main() {
	flag.Parse()

	if *version {
		fmt.Printf("%s v%s - %s\n", AppName, AppVersion, AppDesc)
		os.Exit(0)
	}

	fmt.Printf("Starting %s v%s...\n", AppName, AppVersion)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	zapLogger, err := logger.New(&cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer zapLogger.Sync()

	zapLogger.Info("Starting service",
		zap.String("app_name", AppName),
		zap.String("version", AppVersion),
		zap.String("config_file", *configPath))

	zapLogger.Info("Configuration loaded",
		zap.Int("server_port", cfg.Server.Port),
		zap.String("database_host", cfg.Database.Host),
		zap.Int("database_port", cfg.Database.Port),
		zap.String("database_name", cfg.Database.DBName),
		zap.String("openai_model", cfg.OpenAI.Model))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(cfg.DatabaseDSN(), zapLogger)
	if err != nil {
		zapLogger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	if err := db.RunMigrations("migrations"); err != nil {
		zapLogger.Fatal("Failed to run database migrations", zap.Error(err))
	}

	aiClient, err := openai.New(
		cfg.OpenAI.APIKey,
		cfg.OpenAI.BaseURL,
		cfg.OpenAI.Model,
		cfg.OpenAI.MaxRetries,
		cfg.OpenAI.TimeoutSec,
		zapLogger,
	)
	if err != nil {
		zapLogger.Fatal("Failed to initialize OpenAI client", zap.Error(err))
	}

	zapLogger.Info("Validating OpenAI model...")
	if err := aiClient.ValidateModel(ctx); err != nil {
		zapLogger.Error("Model validation failed, but continuing", zap.Error(err))
	}

	hasher := hash.New(zapLogger)
	usageTracker := tracker.New(db, zapLogger, cfg.Tracker.BatchSize, time.Duration(cfg.Tracker.FlushIntervalSec)*time.Second)
	usageTracker.Start(ctx)
	defer usageTracker.Stop()

	cache := cache.New(db, aiClient, hasher, usageTracker, zapLogger)

	httpServer := server.New(cache, zapLogger)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := httpServer.Start(addr); err != nil && err != http.ErrServerClosed {
			zapLogger.Fatal("Failed to start HTTP server", zap.Error(err))
		}
	}()

	zapLogger.Info("Service started successfully",
		zap.String("address", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
		zap.String("health_check", fmt.Sprintf("http://%s:%d/healthz", cfg.Server.Host, cfg.Server.Port)),
		zap.String("embeddings_endpoint", fmt.Sprintf("http://%s:%d/embed", cfg.Server.Host, cfg.Server.Port)))

	select {
	case sig := <-sigChan:
		zapLogger.Info("Received shutdown signal", zap.String("signal", sig.String()))
	case <-ctx.Done():
		zapLogger.Info("Context cancelled, shutting down")
	}

	zapLogger.Info("Shutting down service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		zapLogger.Error("HTTP server shutdown error", zap.Error(err))
	} else {
		zapLogger.Info("HTTP server shutdown completed")
	}

	zapLogger.Info("Service shutdown completed")
}
