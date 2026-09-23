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

	Storage storage.Config
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

	cfg := Config{
		ListenAddr:          envOr("PAGE_HUB_LISTEN_ADDR", DefaultListenAddr),
		Version:             envOr("PAGE_HUB_VERSION", "dev"),
		AuthAssertionHeader: envOr("PAGE_HUB_AUTH_ASSERTION_HEADER", DefaultAssertionHeader),
		AuthAssertionValue:  os.Getenv("PAGE_HUB_AUTH_ASSERTION_VALUE"),
		AuthLoginURL:        os.Getenv("PAGE_HUB_AUTH_LOGIN_URL"),
		DevAuthBypass:       devBypass,
		CatalogPath:         os.Getenv("PAGE_HUB_CATALOG_PATH"),
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
