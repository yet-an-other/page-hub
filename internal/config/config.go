// Package config owns the small set of runtime settings required by the
// first deployable Page Hub manager.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/yet-an-other/page-hub/internal/storage"
)

const (
	DefaultListenAddr      = "127.0.0.1:8080"
	DefaultAssertionHeader = "X-Page-Hub-Assertion"
	DefaultStorageRegion   = "us-east-1"
)

// NormalizePublicBaseURL validates a canonical public origin and removes a
// trailing slash so canonical URLs join deterministically.
func NormalizePublicBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", errors.New("a public base URL is required to record canonical URLs and run public-route probes")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("public base URL %q must be an absolute http(s) origin", raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", fmt.Errorf("public base URL %q must not contain a query, fragment, or userinfo", raw)
	}
	return trimmed, nil
}

// PublicBaseURLFromEnv reads the canonical public origin from
// PAGE_HUB_PUBLIC_BASE_URL. The adoption plan and commit commands use it to
// record canonical public URLs and to probe the declared public routes, and
// the running manager requires it for canonical public URLs and previews.
func PublicBaseURLFromEnv() (string, error) {
	normalized, err := NormalizePublicBaseURL(os.Getenv("PAGE_HUB_PUBLIC_BASE_URL"))
	if err != nil {
		return "", fmt.Errorf("PAGE_HUB_PUBLIC_BASE_URL is required for plan and commit, such as https://share.bdgn.me: %w", err)
	}
	return normalized, nil
}

// Config is the runtime configuration. Secrets are kept in memory only and
// are never included in health or manager responses.
type Config struct {
	ListenAddr string
	Version    string

	AuthAssertionHeader string
	AuthAssertionValue  string
	AuthLoginURL        string
	DevAuthBypass       bool

	CatalogPath string

	// PublicBaseURL is the canonical public origin Publications are served
	// from. The manager needs it to record canonical public URLs for the
	// inventory and to redirect authenticated previews.
	PublicBaseURL string

	// StorageQuotaBytes is the explicitly configured bucket quota. Usage is
	// always computed from bucket observations, never from a vendor-specific
	// quota interface.
	StorageQuotaBytes int64

	Storage storage.Config
}

// StorageFromEnv builds the S3-compatible connection from the standard
// PAGE_HUB_S3_* environment variables. Administrative subcommands use this so
// they never need manager runtime settings such as the browser assertion.
func StorageFromEnv() storage.Config {
	return storage.Config{
		Endpoint:        os.Getenv("PAGE_HUB_S3_ENDPOINT"),
		Region:          envOr("PAGE_HUB_S3_REGION", DefaultStorageRegion),
		Bucket:          os.Getenv("PAGE_HUB_S3_BUCKET"),
		AccessKeyID:     os.Getenv("PAGE_HUB_S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("PAGE_HUB_S3_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("PAGE_HUB_S3_SESSION_TOKEN"),
	}
}

// Load reads the process environment and validates settings that are unsafe to
// accept at runtime. A missing storage endpoint or bucket is intentionally
// represented as a misconfigured storage status rather than a startup panic so
// the manager can explain degraded configuration without exposing storage data.
func Load() (Config, error) {
	devBypass, err := envBool("PAGE_HUB_DEV_AUTH_BYPASS", false)
	if err != nil {
		return Config{}, err
	}
	quota, err := envInt64("PAGE_HUB_STORAGE_QUOTA_BYTES")
	if err != nil {
		return Config{}, err
	}
	publicBaseURL, err := NormalizePublicBaseURL(os.Getenv("PAGE_HUB_PUBLIC_BASE_URL"))
	if err != nil {
		return Config{}, fmt.Errorf("PAGE_HUB_PUBLIC_BASE_URL is required, such as https://share.bdgn.me: %w", err)
	}

	cfg := Config{
		ListenAddr:          envOr("PAGE_HUB_LISTEN_ADDR", DefaultListenAddr),
		Version:             envOr("PAGE_HUB_VERSION", "dev"),
		AuthAssertionHeader: envOr("PAGE_HUB_AUTH_ASSERTION_HEADER", DefaultAssertionHeader),
		AuthAssertionValue:  os.Getenv("PAGE_HUB_AUTH_ASSERTION_VALUE"),
		AuthLoginURL:        os.Getenv("PAGE_HUB_AUTH_LOGIN_URL"),
		DevAuthBypass:       devBypass,
		CatalogPath:         os.Getenv("PAGE_HUB_CATALOG_PATH"),
		StorageQuotaBytes:   quota,
		PublicBaseURL:       publicBaseURL,
		Storage: storage.Config{
			Endpoint:        os.Getenv("PAGE_HUB_S3_ENDPOINT"),
			Region:          envOr("PAGE_HUB_S3_REGION", DefaultStorageRegion),
			Bucket:          os.Getenv("PAGE_HUB_S3_BUCKET"),
			AccessKeyID:     os.Getenv("PAGE_HUB_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("PAGE_HUB_S3_SECRET_ACCESS_KEY"),
			SessionToken:    os.Getenv("PAGE_HUB_S3_SESSION_TOKEN"),
		},
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks authentication boundary settings and listener safety. It
// does not make a network request or validate credentials.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		return errors.New("PAGE_HUB_LISTEN_ADDR must not be empty")
	}
	if strings.TrimSpace(c.CatalogPath) == "" {
		return errors.New("PAGE_HUB_CATALOG_PATH is required; point it at the private SQLite catalog")
	}
	if c.StorageQuotaBytes <= 0 {
		return errors.New("PAGE_HUB_STORAGE_QUOTA_BYTES is required: configure the exact bucket quota in bytes; usage is calculated from bucket observations, not a vendor quota interface")
	}
	if _, err := NormalizePublicBaseURL(c.PublicBaseURL); err != nil {
		return fmt.Errorf("PAGE_HUB_PUBLIC_BASE_URL is required for canonical public URLs and authenticated previews: %w", err)
	}
	if !validHeaderName(c.AuthAssertionHeader) {
		return fmt.Errorf("invalid authentication assertion header %q", c.AuthAssertionHeader)
	}
	if !c.DevAuthBypass && c.AuthAssertionValue == "" {
		return errors.New("PAGE_HUB_AUTH_ASSERTION_VALUE is required unless development authentication bypass is enabled")
	}
	if c.DevAuthBypass && !IsLoopbackListener(c.ListenAddr) {
		return fmt.Errorf("development authentication bypass requires a loopback listener, got %q", c.ListenAddr)
	}
	if c.AuthLoginURL != "" {
		parsed, err := url.Parse(c.AuthLoginURL)
		validAbsolute := parsed != nil && parsed.IsAbs() && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
		validRelative := parsed != nil && !parsed.IsAbs() && strings.HasPrefix(c.AuthLoginURL, "/") && !strings.HasPrefix(c.AuthLoginURL, "//")
		if err != nil || (!validAbsolute && !validRelative) || strings.ContainsAny(c.AuthLoginURL, "\r\n") {
			return fmt.Errorf("invalid authentication login URL")
		}
	}
	return nil
}

// IsLoopbackListener reports whether an address is local to the machine. Unix
// sockets are local IPC and therefore satisfy the same boundary for the
// explicit development bypass.
func IsLoopbackListener(address string) bool {
	if strings.HasPrefix(address, "unix:") {
		return strings.TrimPrefix(address, "unix:") != ""
	}

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// envInt64 reads a required positive integer setting.
func envInt64(name string) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, fmt.Errorf("%s is required and must be a positive integer number of bytes", name)
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer number of bytes, got %q", name, raw)
	}
	return parsed, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", name, err)
	}
	return parsed, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		default:
			return false
		}
	}
	return true
}
