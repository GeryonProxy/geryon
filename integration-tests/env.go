package integration

import "os"

// env returns environment variable or default value.
func env(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
