package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEnvFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFromEnvOnly(t *testing.T) {
	content := strings.Join([]string{
		"# Complete environment configuration",
		"MUX_PUBLISH_HOST=127.0.0.1",
		"MUX_PUBLISH_PORT=9494",
		"MUX_SERVER_HOST=127.0.0.1",
		"MUX_SERVER_PORT=9393",
		"MUX_SERVER_TLS_CERT_FILE=",
		"MUX_SERVER_TLS_KEY_FILE=",
		"MUX_SERVER_ALLOW_INSECURE_PUBLIC_HTTP=false",
		"MUX_SERVER_ALLOW_UNAUTHENTICATED_PUBLIC_PROXY=false",
		"MUX_SERVER_MAX_CONNECTIONS=12",
		"MUX_SERVER_MAX_CONNECTIONS_PER_IP=6",
		"MUX_SERVER_MAX_HEADER_BYTES=8192",
		"MUX_SERVER_HTTP2_MAX_CONCURRENT_STREAMS=2",
		"MUX_SERVER_HTTP2_SEND_PING_TIMEOUT=100ms",
		"MUX_SERVER_HTTP2_PING_TIMEOUT=200ms",
		"MUX_SERVER_HTTP2_WRITE_BYTE_TIMEOUT=300ms",
		"MUX_SERVER_READ_HEADER_TIMEOUT=3s",
		"MUX_SERVER_IDLE_TIMEOUT=4m",
		"MUX_SERVER_SHUTDOWN_TIMEOUT=5s",
		"MUX_AUTH_ENABLED=true",
		"MUX_AUTH_USERNAME=client",
		"MUX_AUTH_PASSWORD=literal$#secret with spaces",
		"MUX_PROXY_ALGORITHM=random",
		"MUX_PROXY_TIMEOUT=9",
		"MUX_PROXY_NETWORK=tcp4",
		"MUX_PROXY_DIAL_TIMEOUT=2s",
		"MUX_PROXY_DIAL_KEEP_ALIVE=3s",
		"MUX_PROXY_TLS_HANDSHAKE_TIMEOUT=4s",
		"MUX_PROXY_RESPONSE_HEADER_TIMEOUT=5s",
		"MUX_PROXY_RESPONSE_BODY_IDLE_TIMEOUT=7s",
		"MUX_PROXY_IDLE_CONN_TIMEOUT=6s",
		"MUX_PROXY_EXPECT_CONTINUE_TIMEOUT=0s",
		"MUX_PROXY_MAX_IDLE_CONNS=11",
		"MUX_PROXY_MAX_IDLE_CONNS_PER_HOST=3",
		"MUX_PROXY_MAX_CONNS_PER_HOST=4",
		"MUX_PROXY_MAX_TUNNELS=7",
		"MUX_PROXY_MAX_TUNNELS_PER_IP=4",
		"MUX_PROXY_TUNNEL_IDLE_TIMEOUT=8m",
		"MUX_PROXY_FAILOVER_COOLDOWN=6s",
		"MUX_UPSTREAM_COUNT=2",
		"MUX_UPSTREAM_1_URL=http://127.0.0.1:8080",
		"MUX_UPSTREAM_1_AUTH_ENABLED=false",
		"MUX_UPSTREAM_1_AUTH_USERNAME=",
		"MUX_UPSTREAM_1_AUTH_PASSWORD=",
		"MUX_UPSTREAM_1_TLS_CA_FILE=",
		"MUX_UPSTREAM_2_URL=socks5://127.0.0.1:1080",
		"MUX_UPSTREAM_2_AUTH_ENABLED=true",
		"MUX_UPSTREAM_2_AUTH_USERNAME=socks-user",
		"MUX_UPSTREAM_2_AUTH_PASSWORD=socks-password",
		"MUX_UPSTREAM_2_TLS_CA_FILE=",
	}, "\n") + "\n"
	cfg, err := LoadConfigWithEnv(filepath.Join(t.TempDir(), "missing.yaml"), writeEnvFixture(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 9393 || cfg.Server.MaxConnections != 12 || cfg.Server.MaxConnectionsPerIP != 6 || cfg.Server.MaxHeaderBytes != 8192 || cfg.Server.HTTP2MaxConcurrentStreams != 2 || time.Duration(cfg.Server.HTTP2SendPingTimeout) != 100*time.Millisecond || time.Duration(cfg.Server.HTTP2PingTimeout) != 200*time.Millisecond || time.Duration(cfg.Server.HTTP2WriteByteTimeout) != 300*time.Millisecond || time.Duration(cfg.Server.ReadHeaderTimeout) != 3*time.Second || time.Duration(cfg.Server.IdleTimeout) != 4*time.Minute || time.Duration(cfg.Server.ShutdownTimeout) != 5*time.Second {
		t.Fatalf("server overrides not applied: %+v", cfg.Server)
	}
	if !cfg.Auth.Enabled || cfg.Auth.Username != "client" || cfg.Auth.Password != "literal$#secret with spaces" {
		t.Fatal("client authentication overrides not applied")
	}
	if cfg.Proxy.Algorithm != "random" || cfg.Proxy.Timeout != 9 || cfg.Proxy.Network != "tcp4" || time.Duration(cfg.Proxy.DialTimeout) != 2*time.Second || time.Duration(cfg.Proxy.DialKeepAlive) != 3*time.Second || time.Duration(cfg.Proxy.TLSHandshakeTimeout) != 4*time.Second || time.Duration(cfg.Proxy.ResponseHeaderTimeout) != 5*time.Second || time.Duration(cfg.Proxy.ResponseBodyIdleTimeout) != 7*time.Second || time.Duration(cfg.Proxy.IdleConnTimeout) != 6*time.Second || time.Duration(cfg.Proxy.ExpectContinueTimeout) != 0 || cfg.Proxy.MaxIdleConns != 11 || cfg.Proxy.MaxIdleConnsPerHost != 3 || cfg.Proxy.MaxConnsPerHost != 4 || cfg.Proxy.MaxTunnels != 7 || cfg.Proxy.MaxTunnelsPerIP != 4 || time.Duration(cfg.Proxy.TunnelIdleTimeout) != 8*time.Minute || time.Duration(cfg.Proxy.FailoverCooldown) != 6*time.Second {
		t.Fatalf("proxy overrides not applied: %+v", cfg.Proxy)
	}
	if len(cfg.Upstreams) != 2 || cfg.Upstreams[0].URL != "http://127.0.0.1:8080" || cfg.Upstreams[1].URL != "socks5://127.0.0.1:1080" || cfg.Upstreams[1].Auth.Username != "socks-user" || cfg.Upstreams[1].Auth.Password != "socks-password" {
		t.Fatal("upstream overrides not applied")
	}
}

func TestEnvPrecedenceOverYAMLAndProcessOverEnv(t *testing.T) {
	yamlPath := filepath.Join(t.TempDir(), "config.yaml")
	yamlContent := "server:\n  port: 8000\nproxy:\n  algorithm: roundrobin\nupstreams:\n  - url: http://127.0.0.1:8001\n"
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := writeEnvFixture(t, "MUX_SERVER_PORT=9000\nMUX_PROXY_ALGORITHM=random\nMUX_UPSTREAM_COUNT=1\nMUX_UPSTREAM_1_URL=socks4://127.0.0.1:1080\n")
	t.Setenv("MUX_SERVER_PORT", "9001")
	cfg, err := LoadConfigWithEnv(yamlPath, envPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9001 || cfg.Proxy.Algorithm != "random" || len(cfg.Upstreams) != 1 || cfg.Upstreams[0].URL != "socks4://127.0.0.1:1080" {
		t.Fatalf("precedence failed: port=%d algorithm=%q upstream-count=%d", cfg.Server.Port, cfg.Proxy.Algorithm, len(cfg.Upstreams))
	}
}

func TestEnvOverridesTLSPolicyAndUpstreamCA(t *testing.T) {
	cfg := Default()
	values := map[string]string{
		"MUX_SERVER_TLS_CERT_FILE":                      "./test-cert.pem",
		"MUX_SERVER_TLS_KEY_FILE":                       "./test-key.pem",
		"MUX_SERVER_ALLOW_INSECURE_PUBLIC_HTTP":         "true",
		"MUX_SERVER_ALLOW_UNAUTHENTICATED_PUBLIC_PROXY": "true",
		"MUX_UPSTREAM_COUNT":                            "1",
		"MUX_UPSTREAM_1_URL":                            "https://127.0.0.1:8443",
		"MUX_UPSTREAM_1_AUTH_ENABLED":                   "true",
		"MUX_UPSTREAM_1_AUTH_USERNAME":                  "up-user",
		"MUX_UPSTREAM_1_AUTH_PASSWORD":                  "up-password",
		"MUX_UPSTREAM_1_TLS_CA_FILE":                    "./upstream-ca.pem",
	}
	if err := applyEnvOverrides(&cfg, values); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.TLS.CertFile != "./test-cert.pem" || cfg.Server.TLS.KeyFile != "./test-key.pem" || !cfg.Server.AllowInsecurePublicHTTP || !cfg.Server.AllowUnauthenticatedPublicProxy {
		t.Fatal("listener TLS and policy overrides were not applied")
	}
	if len(cfg.Upstreams) != 1 || !cfg.Upstreams[0].Auth.Enabled || cfg.Upstreams[0].Auth.Username != "up-user" || cfg.Upstreams[0].Auth.Password != "up-password" || cfg.Upstreams[0].TLSCAFile != "./upstream-ca.pem" {
		t.Fatal("upstream TLS and authentication overrides were not applied")
	}
}

func TestEnvErrors(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"bad boolean", "MUX_AUTH_ENABLED=maybe\n", "MUX_AUTH_ENABLED"},
		{"bad duration", "MUX_SERVER_IDLE_TIMEOUT=tomorrow\n", "MUX_SERVER_IDLE_TIMEOUT"},
		{"bad count", "MUX_UPSTREAM_COUNT=1000000\n", "MUX_UPSTREAM_COUNT"},
		{"bad network", "MUX_PROXY_NETWORK=udp\n", "proxy.network"},
		{"bad per-IP connection limit", "MUX_SERVER_MAX_CONNECTIONS=4\n", "server.max_connections_per_ip"},
		{"bad stream limit", "MUX_SERVER_HTTP2_MAX_CONCURRENT_STREAMS=1000\n", "server.http2_max_concurrent_streams"},
		{"missing URL", "MUX_UPSTREAM_COUNT=1\n", "MUX_UPSTREAM_1_URL"},
		{"upstream without count", "MUX_UPSTREAM_1_URL=http://127.0.0.1:8080\n", "MUX_UPSTREAM_COUNT"},
		{"unknown field", "MUX_SERVER_PRTO=https\n", "MUX_SERVER_PRTO"},
		{"duplicate field", "MUX_SERVER_PORT=8000\nMUX_SERVER_PORT=9000\n", "duplicate"},
		{"bad assignment", "MUX_SERVER_PORT\n", "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yamlPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(yamlPath, []byte("upstreams:\n  - url: http://127.0.0.1:8080\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfigWithEnv(yamlPath, writeEnvFixture(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMissingYAMLAndEnvFailsClosed(t *testing.T) {
	_, err := LoadConfigWithEnv(filepath.Join(t.TempDir(), "missing.yaml"), filepath.Join(t.TempDir(), "missing.env"))
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("error = %v, want missing config", err)
	}
}
