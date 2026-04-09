package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/zinsserzhang/gobivc/internal/api"
	"github.com/zinsserzhang/gobivc/internal/config"
	"github.com/zinsserzhang/gobivc/internal/service"
	"github.com/zinsserzhang/gobivc/internal/store"
)

func main() {
	cfg := config.Load()

	// Initialize storage
	dataDir := getEnvOrDefault("DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	dbPath := filepath.Join(dataDir, "gobivc.db")
	sqliteStore, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer sqliteStore.Close()

	// Recover any reports that were generating when the server last stopped
	if err := sqliteStore.RecoverPendingReports(); err != nil {
		log.Printf("WARNING: failed to recover pending reports: %v", err)
	}

	// Initialize AI generator
	generator := service.NewClaudeGenerator(
		cfg.AnthropicAPIKey,
		cfg.AnthropicModel,
		cfg.AnthropicBaseURL,
	)

	// Initialize service layer
	reportService := service.NewReportService(sqliteStore, generator)

	// Initialize API handler and router
	handler := api.NewHandler(reportService)
	router := api.NewRouter(handler)

	addr := ":" + cfg.Port
	log.Printf("GobiVC server starting on http://localhost%s", addr)
	log.Printf("Using AI model: %s", cfg.AnthropicModel)
	log.Printf("Database: %s", dbPath)

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
