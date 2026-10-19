// Package config loads application configuration from environment variables,
// matching the keys documented in .env.example.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv   string
	HTTPAddr string

	DatabaseURL string

	RedisURL           string
	RedisStream        string
	RedisConsumerGroup string

	DeliveryConnectTimeout time.Duration
	DeliveryRequestTimeout time.Duration
	DeliveryMaxAttempts    int
	DeliveryBaseBackoff    time.Duration
	DeliveryMaxBackoff     time.Duration

	PayloadMaxBytes         int64
	ResponseCaptureMaxBytes int64

	CircuitFailureThreshold int
	CircuitOpenCooldown     time.Duration

	AllowPrivateTargets bool

	OutboxPollInterval       time.Duration
	WorkerCount              int
	WorkerTargetConcurrency  int
	HistoryRetentionAge      time.Duration
	HistoryRetentionInterval time.Duration
	HistoryRetentionBatch    int

	// EventsMaxPending is the admission-control bound on durable pending
	// event rows (EVENTS_MAX_PENDING_EVENTS). 0 disables the limit, which
	// is the default so existing deployments behave unchanged.
	EventsMaxPending int

	AuthEnabled bool
	APIKey      string

	// SecretEncryptionKey is a 32-byte key used to encrypt target secrets
	// at rest (AES-256-GCM). It must be provided via SECRET_ENCRYPTION_KEY
	// as base64, e.g. generated with `openssl rand -base64 32`.
	SecretEncryptionKey []byte
}

// Load reads configuration from environment variables, applying the same
// defaults documented in .env.example.
func Load() (Config, error) {
	cfg := Config{
		AppEnv:   getEnv("APP_ENV", "development"),
		HTTPAddr: getEnv("HTTP_ADDR", ":8080"),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://webhook:webhook@localhost:5432/webhook?sslmode=disable"),

		RedisURL:           getEnv("REDIS_URL", "redis://localhost:6379/0"),
		RedisStream:        getEnv("REDIS_STREAM", "webhook:deliveries"),
		RedisConsumerGroup: getEnv("REDIS_CONSUMER_GROUP", "webhook-workers"),

		APIKey: getEnv("API_KEY", ""),
	}

	var err error
	if cfg.AllowPrivateTargets, err = getEnvBool("ALLOW_PRIVATE_TARGETS", false); err != nil {
		return Config{}, err
	}
	if cfg.AuthEnabled, err = getEnvBool("AUTH_ENABLED", false); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryConnectTimeout, err = getEnvDuration("DELIVERY_CONNECT_TIMEOUT", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryRequestTimeout, err = getEnvDuration("DELIVERY_REQUEST_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryMaxAttempts, err = getEnvInt("DELIVERY_MAX_ATTEMPTS", 5); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryBaseBackoff, err = getEnvDuration("DELIVERY_BASE_BACKOFF", 1*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryMaxBackoff, err = getEnvDuration("DELIVERY_MAX_BACKOFF", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.PayloadMaxBytes, err = getEnvInt64("PAYLOAD_MAX_BYTES", 262144); err != nil {
		return Config{}, err
	}
	if cfg.ResponseCaptureMaxBytes, err = getEnvInt64("RESPONSE_CAPTURE_MAX_BYTES", 16384); err != nil {
		return Config{}, err
	}
	if cfg.CircuitFailureThreshold, err = getEnvInt("CIRCUIT_FAILURE_THRESHOLD", 5); err != nil {
		return Config{}, err
	}
	if cfg.CircuitOpenCooldown, err = getEnvDuration("CIRCUIT_OPEN_COOLDOWN", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.OutboxPollInterval, err = getEnvDuration("OUTBOX_POLL_INTERVAL", 500*time.Millisecond); err != nil {
		return Config{}, err
	}
	if cfg.WorkerCount, err = getEnvInt("WORKER_COUNT", 4); err != nil {
		return Config{}, err
	}
	if cfg.WorkerTargetConcurrency, err = getEnvInt("WORKER_TARGET_CONCURRENCY", 0); err != nil {
		return Config{}, err
	}
	if cfg.EventsMaxPending, err = getEnvInt("EVENTS_MAX_PENDING_EVENTS", 0); err != nil {
		return Config{}, err
	}
	if cfg.HistoryRetentionAge, err = getEnvDuration("HISTORY_RETENTION_AGE", 0); err != nil {
		return Config{}, err
	}
	if cfg.HistoryRetentionInterval, err = getEnvDuration("HISTORY_RETENTION_INTERVAL", time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.HistoryRetentionBatch, err = getEnvInt("HISTORY_RETENTION_BATCH", 100); err != nil {
		return Config{}, err
	}
	if cfg.HistoryRetentionAge < 0 || (cfg.HistoryRetentionAge > 0 && cfg.HistoryRetentionAge < 24*time.Hour) || cfg.HistoryRetentionInterval < time.Second || cfg.HistoryRetentionBatch < 1 || cfg.HistoryRetentionBatch > 1000 {
		return Config{}, fmt.Errorf("retention requires age 0 or >=24h, interval >=1s and batch 1..1000")
	}

	if cfg.AppEnv != "development" && !cfg.AuthEnabled {
		return Config{}, fmt.Errorf("AUTH_ENABLED must be true outside development")
	}
	if cfg.AppEnv != "development" && cfg.AllowPrivateTargets {
		return Config{}, fmt.Errorf("ALLOW_PRIVATE_TARGETS is restricted to development")
	}
	if cfg.AuthEnabled && cfg.APIKey == "" {
		return Config{}, fmt.Errorf("AUTH_ENABLED is true but API_KEY is empty")
	}
	if cfg.PayloadMaxBytes <= 0 || cfg.ResponseCaptureMaxBytes <= 0 {
		return Config{}, fmt.Errorf("payload and response capture limits must be positive")
	}
	if cfg.EventsMaxPending < 0 {
		return Config{}, fmt.Errorf("EVENTS_MAX_PENDING_EVENTS must be >= 0 (0 disables the limit)")
	}
	if cfg.DeliveryConnectTimeout <= 0 || cfg.DeliveryRequestTimeout <= 0 {
		return Config{}, fmt.Errorf("delivery timeouts must be positive")
	}
	if cfg.DeliveryRequestTimeout > time.Minute || cfg.DeliveryConnectTimeout > cfg.DeliveryRequestTimeout {
		return Config{}, fmt.Errorf("delivery request timeout must be <= 1m and connect timeout must not exceed it")
	}
	if cfg.WorkerCount <= 0 || cfg.OutboxPollInterval <= 0 || cfg.DeliveryMaxAttempts <= 0 || cfg.DeliveryMaxAttempts > 100 {
		return Config{}, fmt.Errorf("worker count and poll interval must be positive; max attempts must be between 1 and 100")
	}
	if cfg.WorkerTargetConcurrency < 0 || cfg.WorkerTargetConcurrency > cfg.WorkerCount {
		return Config{}, fmt.Errorf("WORKER_TARGET_CONCURRENCY must be between 0 and WORKER_COUNT")
	}
	if cfg.DeliveryBaseBackoff < 0 || cfg.DeliveryMaxBackoff <= 0 || cfg.DeliveryBaseBackoff > cfg.DeliveryMaxBackoff {
		return Config{}, fmt.Errorf("retry backoff must satisfy 0 <= base <= max and max > 0")
	}
	if cfg.CircuitFailureThreshold <= 0 || cfg.CircuitOpenCooldown <= 0 {
		return Config{}, fmt.Errorf("circuit threshold and cooldown must be positive")
	}

	rawKey := getEnv("SECRET_ENCRYPTION_KEY", "")
	if rawKey == "" {
		return Config{}, fmt.Errorf("SECRET_ENCRYPTION_KEY is required (base64-encoded 32-byte key)")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("invalid SECRET_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return Config{}, fmt.Errorf("SECRET_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	cfg.SecretEncryptionKey = key

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", key, err)
	}
	return b, nil
}

func getEnvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return n, nil
}

func getEnvInt64(key string, fallback int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return n, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return d, nil
}
