package config

import (
	"log"
	"os"
)

// Config holds application configuration.
type Config struct {
	Port             string
	DataDir          string
	AnthropicAPIKey  string
	AnthropicModel   string
	AnthropicBaseURL string
	APIToken         string // Optional: if set, API routes require Bearer <token>
	AllowedOrigin    string // CORS allowed origin
}

// Load reads configuration from environment variables.
func Load() *Config {
	cfg := &Config{
		Port:             getEnv("PORT", "8080"),
		DataDir:          getEnv("DATA_DIR", "./data"),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   getEnv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
		AnthropicBaseURL: getEnv("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		APIToken:         os.Getenv("API_TOKEN"),
		AllowedOrigin:    getEnv("ALLOWED_ORIGIN", "*"),
	}

	if cfg.AnthropicAPIKey == "" {
		log.Println("WARNING: ANTHROPIC_API_KEY is not set. Report generation will fail.")
	}

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
