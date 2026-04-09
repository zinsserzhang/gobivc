package main

import (
	"log"
	"net/http"

	"github.com/zinsserzhang/gobivc/internal/api"
	"github.com/zinsserzhang/gobivc/internal/config"
	"github.com/zinsserzhang/gobivc/internal/service"
	"github.com/zinsserzhang/gobivc/internal/store"
)

func main() {
	cfg := config.Load()

	// Initialize storage
	reportStore := store.NewMemoryStore()

	// Initialize AI generator
	generator := service.NewClaudeGenerator(
		cfg.AnthropicAPIKey,
		cfg.AnthropicModel,
		cfg.AnthropicBaseURL,
	)

	// Initialize service layer
	reportService := service.NewReportService(reportStore, generator)

	// Initialize API handler and router
	handler := api.NewHandler(reportService)
	router := api.NewRouter(handler)

	addr := ":" + cfg.Port
	log.Printf("GobiVC server starting on http://localhost%s", addr)
	log.Printf("Using AI model: %s", cfg.AnthropicModel)

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
