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
		{name: "invalid network", yaml: "proxy:\n  network: udp\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "proxy.network"},
		{name: "invalid auth threshold", yaml: "auth:\n  max_failed_attempts: 0\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "auth.max_failed_attempts"},
		{name: "invalid auth window", yaml: "auth:\n  failure_window: 0s\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "auth.failure_window"},
		{name: "invalid tracked IP limit", yaml: "auth:\n  max_tracked_ips: 0\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "auth.max_tracked_ips"},
		{name: "invalid dial timeout", yaml: "proxy:\n  dial_timeout: 0s\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "proxy transport timeouts"},
		{name: "invalid response body idle timeout", yaml: "proxy:\n  response_body_idle_timeout: 0s\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "proxy.response_body_idle_timeout"},
		{name: "per-IP connection limit too high", yaml: "server:\n  max_connections_per_ip: 2048\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "server.max_connections_per_ip"},
		{name: "header limit too small", yaml: "server:\n  max_header_bytes: 32\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "server.max_header_bytes"},
		{name: "HTTP2 streams exceed tunnels", yaml: "server:\n  http2_max_concurrent_streams: 512\nupstreams:\n  - url: http://127.0.0.1:8888\n", wantError: "server.http2_max_concurrent_streams"},
		{name: "bad upstream scheme", yaml: "upstreams:\n  - url: ftp://127.0.0.1:8888\n", wantError: "scheme"},
		{name: "socks4 USERID", yaml: "upstreams:\n  - url: socks4://127.0.0.1:1080\n    auth:\n      enabled: true\n      username: test-user\n"},
		{name: "socks4 password rejected", yaml: "upstreams:\n  - url: socks4://127.0.0.1:1080\n    auth:\n      enabled: true\n      username: test-user\n      password: test-password\n", wantError: "SOCKS4 accepts"},
		{name: "socks5 credentials", yaml: "upstreams:\n  - url: socks5://127.0.0.1:1080\n    auth:\n      enabled: true\n      username: test-user\n      password: test-password\n"},
		{name: "socks5 empty password", yaml: "upstreams:\n  - url: socks5://127.0.0.1:1080\n    auth:\n      enabled: true\n      username: test-user\n", wantError: "SOCKS5 requires"},
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
			if cfg.Proxy.MaxTunnels != 256 || time.Duration(cfg.Proxy.FailoverCooldown) != 30*time.Second || time.Duration(cfg.Proxy.ResponseBodyIdleTimeout) != 2*time.Minute {
				t.Fatalf("unexpected defaults: %+v", cfg.Proxy)
			}
			if cfg.Auth.MaxFailedAttempts != 10 || time.Duration(cfg.Auth.FailureWindow) != time.Minute || time.Duration(cfg.Auth.BlockDuration) != 5*time.Minute || cfg.Auth.MaxTrackedIPs != 4096 {
				t.Fatalf("unexpected auth defaults: %+v", cfg.Auth)
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
		{"socks4 default port", UpstreamConfig{URL: "socks4://localhost"}, "localhost:1080", false},
		{"socks5 default port", UpstreamConfig{URL: "socks5://localhost"}, "localhost:1080", false},
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
