package config

import (
	"log"
	"os"
	"strings"
)

// AIProvider identifies which AI backend to use.
type AIProvider string

const (
	ProviderClaude AIProvider = "claude"
	ProviderOpenAI AIProvider = "openai" // OpenAI-compatible: MiniMax, DeepSeek, etc.
)

// Config holds application configuration.
type Config struct {
	Port          string
	DataDir       string
	APIToken      string // Optional: if set, API routes require Bearer <token>
	AllowedOrigin string // CORS allowed origin

	// AI provider settings
	AIProvider AIProvider
	AIAPIKey   string
	AIModel    string
	AIBaseURL  string

	// Feishu integration (via lark-cli, no config needed here)
}

// Load reads configuration from environment variables.
func Load() *Config {
	provider := AIProvider(strings.ToLower(getEnv("AI_PROVIDER", "openai")))

	cfg := &Config{
		Port:          getEnv("PORT", "8080"),
		DataDir:       getEnv("DATA_DIR", "./data"),
		APIToken:      os.Getenv("API_TOKEN"),
		AllowedOrigin: getEnv("ALLOWED_ORIGIN", "*"),
		AIProvider:    provider,
	}

	switch provider {
	case ProviderClaude:
		cfg.AIAPIKey = os.Getenv("ANTHROPIC_API_KEY")
		cfg.AIModel = getEnv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514")
		cfg.AIBaseURL = getEnv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
		if cfg.AIAPIKey == "" {
			log.Println("WARNING: ANTHROPIC_API_KEY is not set.")
		}
	default:
		// OpenAI-compatible (MiniMax, DeepSeek, OpenAI, etc.)
		cfg.AIProvider = ProviderOpenAI
		cfg.AIAPIKey = firstNonEmpty(os.Getenv("AI_API_KEY"), os.Getenv("ANTHROPIC_API_KEY"))
		cfg.AIModel = firstNonEmpty(os.Getenv("AI_MODEL"), os.Getenv("ANTHROPIC_MODEL"), "MiniMax-M2.7")
		cfg.AIBaseURL = firstNonEmpty(os.Getenv("AI_BASE_URL"), os.Getenv("ANTHROPIC_BASE_URL"), "https://api.minimaxi.com/v1")
		if cfg.AIAPIKey == "" {
			log.Println("WARNING: AI_API_KEY is not set.")
		}
	}

	log.Printf("INFO: AI Provider=%s, Model=%s, BaseURL=%s", cfg.AIProvider, cfg.AIModel, cfg.AIBaseURL)

	if cfg.APIToken == "" {
		log.Println("WARNING: API_TOKEN is not set. API is publicly accessible.")
	} else {
		log.Println("INFO: API authentication enabled")
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
