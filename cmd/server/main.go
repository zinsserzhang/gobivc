package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zinsserzhang/gobivc/internal/api"
	"github.com/zinsserzhang/gobivc/internal/config"
	"github.com/zinsserzhang/gobivc/internal/feishu"
	"github.com/zinsserzhang/gobivc/internal/service"
	"github.com/zinsserzhang/gobivc/internal/store"
)

func main() {
	cfg := config.Load()

	// Initialize storage
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "gobivc.db")
	sqliteStore, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer sqliteStore.Close()

	if err := sqliteStore.RecoverPendingReports(); err != nil {
		log.Printf("WARNING: failed to recover pending reports: %v", err)
	}

	// Initialize AI generator
	var generator service.AIGenerator
	switch cfg.AIProvider {
	case config.ProviderClaude:
		generator = service.NewClaudeGenerator(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
	default:
		generator = service.NewOpenAIGenerator(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
	}

	// Initialize Feishu client (uses lark-cli)
	feishuClient := feishu.NewClient(cfg.FeishuFolderToken)
	feishuClient.CheckAvailable(context.Background())
	api.FeishuEnabled = feishuClient.IsConfigured()

	// Initialize service layer
	reportService := service.NewReportService(sqliteStore, generator, feishuClient)

	// Initialize API handler and router
	handler := api.NewHandler(reportService)
	router := api.NewRouter(handler)

	httpHandler := api.Chain(
		router,
		api.RecoverMiddleware,
		api.LoggingMiddleware,
		api.CORSMiddleware(cfg.AllowedOrigin),
		api.AuthMiddleware(cfg.APIToken),
	)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		log.Printf("GobiVC server starting on http://localhost%s", addr)
		log.Printf("AI provider: %s, model: %s", cfg.AIProvider, cfg.AIModel)
		log.Printf("Database: %s", dbPath)
		if feishuClient.IsConfigured() {
			log.Printf("Feishu integration: enabled")
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
