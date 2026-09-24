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
