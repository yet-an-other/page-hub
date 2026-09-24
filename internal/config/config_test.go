package config

import (
	"strings"
	"testing"
)

func TestDevelopmentBypassRequiresLoopbackListener(t *testing.T) {
	for _, test := range []struct {
		name       string
		listenAddr string
		wantErr    bool
	}{
		{name: "loopback ipv4", listenAddr: "127.0.0.1:8080"},
		{name: "loopback hostname", listenAddr: "localhost:8080"},
		{name: "loopback ipv6", listenAddr: "[::1]:8080"},
		{name: "unix socket", listenAddr: "unix:/run/page-hub/page-hub.sock"},
		{name: "all interfaces", listenAddr: ":8080", wantErr: true},
		{name: "private interface", listenAddr: "192.0.2.10:8080", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{
				ListenAddr:          test.listenAddr,
				DevAuthBypass:       true,
				AuthAssertionHeader: "X-Page-Hub-Assertion",
				CatalogPath:         "/var/lib/page-hub/catalog.db",
				StorageQuotaBytes:   1 << 30,
			}
			if err := cfg.Validate(); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestProductionRequiresAnAssertion(t *testing.T) {
	cfg := Config{
		ListenAddr:          "127.0.0.1:8080",
		AuthAssertionHeader: "X-Page-Hub-Assertion",
		CatalogPath:         "/var/lib/page-hub/catalog.db",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() succeeded without a production assertion")
	}
}

func TestProductionAcceptsConfiguredAssertion(t *testing.T) {
	cfg := Config{
		ListenAddr:          "127.0.0.1:8080",
		AuthAssertionHeader: "X-Page-Hub-Assertion",
		AuthAssertionValue:  "configured-value",
		CatalogPath:         "/var/lib/page-hub/catalog.db",
		StorageQuotaBytes:   1 << 30,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestProductionRequiresACatalogPath(t *testing.T) {
	cfg := Config{
		ListenAddr:          "127.0.0.1:8080",
		AuthAssertionHeader: "X-Page-Hub-Assertion",
		AuthAssertionValue:  "configured-value",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() succeeded without a catalog path")
	}
}

func TestValidateRequiresPositiveStorageQuota(t *testing.T) {
	for name, quota := range map[string]int64{
		"missing":    0,
		"negative":   -1,
		"reasonable": 1 << 30,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				ListenAddr:          "127.0.0.1:8080",
				AuthAssertionHeader: "X-Page-Hub-Assertion",
				AuthAssertionValue:  "configured-value",
				CatalogPath:         "/var/lib/page-hub/catalog.db",
				StorageQuotaBytes:   quota,
			}
			err := cfg.Validate()
			if (err != nil) != (quota <= 0) {
				t.Fatalf("Validate() error = %v, quota %d", err, quota)
			}
		})
	}
}

func TestLoadRequiresStorageQuota(t *testing.T) {
	t.Setenv("PAGE_HUB_LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("PAGE_HUB_AUTH_ASSERTION_VALUE", "test-only-assertion")
	t.Setenv("PAGE_HUB_CATALOG_PATH", "/tmp/test-catalog.db")

	t.Setenv("PAGE_HUB_STORAGE_QUOTA_BYTES", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PAGE_HUB_STORAGE_QUOTA_BYTES") {
		t.Fatalf("Load() without quota error = %v", err)
	}

	for name, value := range map[string]string{
		"zero":     "0",
		"negative": "-5",
		"float":    "1.5",
		"text":     "1GiB",
	} {
		t.Setenv("PAGE_HUB_STORAGE_QUOTA_BYTES", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PAGE_HUB_STORAGE_QUOTA_BYTES") {
			t.Fatalf("Load() with %s quota (%q) error = %v", name, value, err)
		}
	}

	t.Setenv("PAGE_HUB_STORAGE_QUOTA_BYTES", "1073741824")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StorageQuotaBytes != 1<<30 {
		t.Fatalf("StorageQuotaBytes = %d, want %d", cfg.StorageQuotaBytes, int64(1<<30))
	}
}

func TestPublicBaseURLFromEnv(t *testing.T) {
	t.Setenv("PAGE_HUB_PUBLIC_BASE_URL", "")
	if _, err := PublicBaseURLFromEnv(); err == nil {
		t.Fatal("PublicBaseURLFromEnv() should fail without PAGE_HUB_PUBLIC_BASE_URL")
	}

	valid := map[string]string{
		"canonical origin": "https://share.bdgn.me",
		"trailing slash":   "https://share.bdgn.me/",
		"http origin":      "http://127.0.0.1:8080",
	}
	for name, value := range valid {
		t.Setenv("PAGE_HUB_PUBLIC_BASE_URL", value)
		got, err := PublicBaseURLFromEnv()
		if err != nil {
			t.Errorf("%s: PublicBaseURLFromEnv() error = %v", name, err)
			continue
		}
		if strings.HasSuffix(got, "/") {
			t.Errorf("%s: base URL %q keeps a trailing slash", name, got)
		}
	}

	invalid := map[string]string{
		"relative": "share.bdgn.me",
		"not http": "ftp://share.bdgn.me",
		"query":    "https://share.bdgn.me/?x=1",
		"fragment": "https://share.bdgn.me/#top",
		"userinfo": "https://user:pass@share.bdgn.me",
	}
	for name, value := range invalid {
		t.Setenv("PAGE_HUB_PUBLIC_BASE_URL", value)
		if _, err := PublicBaseURLFromEnv(); err == nil {
			t.Errorf("%s: PublicBaseURLFromEnv() should reject %q", name, value)
		}
	}
}
