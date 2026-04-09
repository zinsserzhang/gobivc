package config

import (
	"log"
	"os"
)

// Config holds application configuration.
type Config struct {
	Port           string
	AnthropicAPIKey string
	AnthropicModel  string
	AnthropicBaseURL string
}

// Load reads configuration from environment variables.
func Load() *Config {
	cfg := &Config{
		Port:             getEnv("PORT", "8080"),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:   getEnv("ANTHROPIC_MODEL", "claude-sonnet-4-20250514"),
		AnthropicBaseURL: getEnv("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
	}

	if cfg.AnthropicAPIKey == "" {
		log.Println("WARNING: ANTHROPIC_API_KEY is not set. Report generation will fail.")
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
