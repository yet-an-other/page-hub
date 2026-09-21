package config

import "testing"

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
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
