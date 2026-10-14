package httpx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// GetEnv returns the value of key, or def when unset or empty.
func GetEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// GetEnvInt parses key as an int, returning def when unset or empty.
func GetEnvInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

// GetEnvBool parses key as a bool ("1", "true", "yes", "on" are true, any
// other non-empty value is false), returning def when unset or empty.
func GetEnvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// GetEnvDuration parses key as a Go duration ("5s", "1m"), returning def when
// unset or empty.
func GetEnvDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a Go duration: %w", key, err)
	}
	return d, nil
}

// RequireEnv returns the value of key or an error when it is unset or empty.
func RequireEnv(key string) (string, error) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("config: %s is required", key)
}
