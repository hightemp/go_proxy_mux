package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{name: "defaults", yaml: "server:\n  host: 127.0.0.1\n  port: 8380\nupstreams:\n  - url: http://127.0.0.1:8888\n"},
		{name: "unknown key", yaml: "server:\n  host: 127.0.0.1\n  typo: true\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "typo"},
		{name: "public HTTP", yaml: "server:\n  host: 0.0.0.0\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "public HTTP listener"},
		{name: "invalid algorithm", yaml: "proxy:\n  algorithm: mystery\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "proxy.algorithm"},
		{name: "bad upstream scheme", yaml: "upstreams:\n  - url: socks5://127.0.0.1:8888\n", wantError: "scheme"},
		{name: "demo credentials", yaml: "auth:\n  enabled: true\n  username: admin\n  password: password\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "demonstration"},
		{name: "missing certificate", yaml: "server:\n  tls:\n    cert_file: /no/such/cert.pem\n    key_file: /no/such/key.pem\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "load server TLS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("LoadConfig error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Proxy.MaxTunnels != 256 || time.Duration(cfg.Proxy.FailoverCooldown) != 30*time.Second {
				t.Fatalf("unexpected defaults: %+v", cfg.Proxy)
			}
		})
	}
}

func TestUpstreamURL(t *testing.T) {
	tests := []struct {
		name      string
		input     UpstreamConfig
		wantHost  string
		wantError bool
	}{
		{"http default port", UpstreamConfig{URL: "http://localhost"}, "localhost:80", false},
		{"https default port", UpstreamConfig{URL: "https://localhost"}, "localhost:443", false},
		{"userinfo denied", UpstreamConfig{URL: "http://user:pass@localhost:8888"}, "", true},
		{"path denied", UpstreamConfig{URL: "http://localhost:8888/path"}, "", true},
		{"invalid port", UpstreamConfig{URL: "http://localhost:99999"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := UpstreamURL(tt.input)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Host != tt.wantHost {
				t.Fatalf("host = %q, want %q", parsed.Host, tt.wantHost)
			}
		})
	}
}
