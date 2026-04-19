package config

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	Port             string
	AllowedOrigins   []string
	FileStoragePath   string `env:"FILE_STORAGE_PATH" default:"./uploads"`
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	origins := os.Getenv("ALLOWED_ORIGINS")
	var allowedOrigins []string
	if origins != "" {
		allowedOrigins = splitOrigins(origins)
	}

	fileStoragePath := os.Getenv("FILE_STORAGE_PATH")
	if fileStoragePath == "" {
		fileStoragePath = "./uploads"
	}

	return Config{
		Port:            port,
		AllowedOrigins: allowedOrigins,
		FileStoragePath: fileStoragePath,
	}
}

// splitOrigins splits a comma-separated origins string and trims whitespace.
func splitOrigins(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, strings.ToLower(trimmed))
		}
	}
	return result
}

// OriginValidator validates WebSocket upgrade requests against an allowlist.
// Supports exact matches and wildcard patterns (e.g., "*.example.com").
type OriginValidator struct {
	allowedOrigins []string
	wildcards      []string
	mu             sync.RWMutex
}

// NewOriginValidator creates a validator from a list of allowed origins.
// Patterns starting with "*." match any subdomain of the given domain.
func NewOriginValidator(allowedOrigins []string) *OriginValidator {
	v := &OriginValidator{}
	for _, origin := range allowedOrigins {
		origin = strings.ToLower(strings.TrimSpace(origin))
		if strings.HasPrefix(origin, "*.") {
			v.wildcards = append(v.wildcards, origin[2:])
		} else {
			v.allowedOrigins = append(v.allowedOrigins, origin)
		}
	}
	return v
}

// CheckOrigin returns true if the request's Origin header is allowed.
// If no origins are configured, it defaults to allowing localhost/127.0.0.1
// for development convenience.
func (v *OriginValidator) CheckOrigin(r *http.Request) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	origin := strings.ToLower(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	if len(v.allowedOrigins) == 0 && len(v.wildcards) == 0 {
		return isLocalhost(origin)
	}

	for _, allowed := range v.allowedOrigins {
		if origin == allowed {
			return true
		}
	}

	for _, wc := range v.wildcards {
		if strings.HasSuffix(origin, wc) {
			return true
		}
	}

	log.Printf("⚠️  Rejected WebSocket origin: %s", origin)
	return false
}

// UpdateAllowedOrigins replaces the allowed origins list at runtime (thread-safe).
func (v *OriginValidator) UpdateAllowedOrigins(origins []string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.allowedOrigins = nil
	v.wildcards = nil

	for _, origin := range origins {
		origin = strings.ToLower(strings.TrimSpace(origin))
		if strings.HasPrefix(origin, "*.") {
			v.wildcards = append(v.wildcards, origin[2:])
		} else {
			v.allowedOrigins = append(v.allowedOrigins, origin)
		}
	}
}

// isLocalhost returns true for common localhost origins used in development.
func isLocalhost(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "http://[::1]")
}

// String returns a human-readable description of the validator state.
func (v *OriginValidator) String() string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var parts []string
	if len(v.allowedOrigins) > 0 {
		parts = append(parts, fmt.Sprintf("exact: %v", v.allowedOrigins))
	}
	if len(v.wildcards) > 0 {
		parts = append(parts, fmt.Sprintf("wildcard: *.%v", v.wildcards))
	}
	if len(parts) == 0 {
		return "localhost-only (development mode)"
	}
	return strings.Join(parts, ", ")
}
