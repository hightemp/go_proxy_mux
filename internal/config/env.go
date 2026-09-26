package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func readEnvValues(path string) (map[string]string, error) {
	values := make(map[string]string)
	if path != "" {
		file, err := os.Open(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read env file: %w", err)
		}
		if err == nil {
			defer func() { _ = file.Close() }()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 0, 4096), 1<<20)
			for lineNumber := 1; scanner.Scan(); lineNumber++ {
				line := strings.TrimSuffix(scanner.Text(), "\r")
				if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				key = strings.TrimSpace(key)
				if !ok || !validEnvName(key) {
					return nil, fmt.Errorf("invalid env file assignment on line %d", lineNumber)
				}
				if !strings.HasPrefix(key, "MUX_") {
					continue
				}
				if _, exists := values[key]; exists {
					return nil, fmt.Errorf("duplicate env variable %s", key)
				}
				values[key] = value
			}
			if err := scanner.Err(); err != nil {
				return nil, fmt.Errorf("read env file: %w", err)
			}
		}
	}
	for _, assignment := range os.Environ() {
		key, value, ok := strings.Cut(assignment, "=")
		if ok && strings.HasPrefix(key, "MUX_") {
			values[key] = value
		}
	}
	return values, nil
}

func validEnvName(name string) bool {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for _, character := range name[1:] {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func applyEnvOverrides(cfg *Config, values map[string]string) error {
	known := map[string]bool{
		"MUX_PUBLISH_HOST":   true,
		"MUX_PUBLISH_PORT":   true,
		"MUX_UPSTREAM_COUNT": true,
	}
	stringsToApply := []struct {
		name string
		dest *string
	}{
		{"MUX_SERVER_HOST", &cfg.Server.Host},
		{"MUX_SERVER_TLS_CERT_FILE", &cfg.Server.TLS.CertFile},
		{"MUX_SERVER_TLS_KEY_FILE", &cfg.Server.TLS.KeyFile},
		{"MUX_AUTH_USERNAME", &cfg.Auth.Username},
		{"MUX_AUTH_PASSWORD", &cfg.Auth.Password},
		{"MUX_PROXY_ALGORITHM", &cfg.Proxy.Algorithm},
		{"MUX_PROXY_NETWORK", &cfg.Proxy.Network},
	}
	for _, entry := range stringsToApply {
		known[entry.name] = true
		if value, ok := values[entry.name]; ok {
			*entry.dest = value
		}
	}
	booleans := []struct {
		name string
		dest *bool
	}{
		{"MUX_SERVER_ALLOW_INSECURE_PUBLIC_HTTP", &cfg.Server.AllowInsecurePublicHTTP},
		{"MUX_SERVER_ALLOW_UNAUTHENTICATED_PUBLIC_PROXY", &cfg.Server.AllowUnauthenticatedPublicProxy},
		{"MUX_AUTH_ENABLED", &cfg.Auth.Enabled},
	}
	for _, entry := range booleans {
		known[entry.name] = true
		if value, ok := values[entry.name]; ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("parse %s as boolean: %w", entry.name, err)
			}
			*entry.dest = parsed
		}
	}
	integers := []struct {
		name string
		dest *int
	}{
		{"MUX_SERVER_PORT", &cfg.Server.Port},
		{"MUX_SERVER_MAX_CONNECTIONS", &cfg.Server.MaxConnections},
		{"MUX_SERVER_MAX_CONNECTIONS_PER_IP", &cfg.Server.MaxConnectionsPerIP},
		{"MUX_SERVER_MAX_HEADER_BYTES", &cfg.Server.MaxHeaderBytes},
		{"MUX_SERVER_HTTP2_MAX_CONCURRENT_STREAMS", &cfg.Server.HTTP2MaxConcurrentStreams},
		{"MUX_PROXY_TIMEOUT", &cfg.Proxy.Timeout},
		{"MUX_AUTH_MAX_FAILED_ATTEMPTS", &cfg.Auth.MaxFailedAttempts},
		{"MUX_AUTH_MAX_TRACKED_IPS", &cfg.Auth.MaxTrackedIPs},
		{"MUX_PROXY_MAX_IDLE_CONNS", &cfg.Proxy.MaxIdleConns},
		{"MUX_PROXY_MAX_IDLE_CONNS_PER_HOST", &cfg.Proxy.MaxIdleConnsPerHost},
		{"MUX_PROXY_MAX_CONNS_PER_HOST", &cfg.Proxy.MaxConnsPerHost},
		{"MUX_PROXY_MAX_TUNNELS", &cfg.Proxy.MaxTunnels},
		{"MUX_PROXY_MAX_TUNNELS_PER_IP", &cfg.Proxy.MaxTunnelsPerIP},
	}
	for _, entry := range integers {
		known[entry.name] = true
		if value, ok := values[entry.name]; ok {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("parse %s as integer: %w", entry.name, err)
			}
			*entry.dest = parsed
		}
	}
	durations := []struct {
		name string
		dest *Duration
	}{
		{"MUX_SERVER_READ_HEADER_TIMEOUT", &cfg.Server.ReadHeaderTimeout},
		{"MUX_SERVER_IDLE_TIMEOUT", &cfg.Server.IdleTimeout},
		{"MUX_SERVER_SHUTDOWN_TIMEOUT", &cfg.Server.ShutdownTimeout},
		{"MUX_SERVER_HTTP2_SEND_PING_TIMEOUT", &cfg.Server.HTTP2SendPingTimeout},
		{"MUX_SERVER_HTTP2_PING_TIMEOUT", &cfg.Server.HTTP2PingTimeout},
		{"MUX_SERVER_HTTP2_WRITE_BYTE_TIMEOUT", &cfg.Server.HTTP2WriteByteTimeout},
		{"MUX_PROXY_DIAL_TIMEOUT", &cfg.Proxy.DialTimeout},
		{"MUX_PROXY_DIAL_KEEP_ALIVE", &cfg.Proxy.DialKeepAlive},
		{"MUX_PROXY_TLS_HANDSHAKE_TIMEOUT", &cfg.Proxy.TLSHandshakeTimeout},
		{"MUX_PROXY_RESPONSE_HEADER_TIMEOUT", &cfg.Proxy.ResponseHeaderTimeout},
		{"MUX_PROXY_RESPONSE_BODY_IDLE_TIMEOUT", &cfg.Proxy.ResponseBodyIdleTimeout},
		{"MUX_PROXY_IDLE_CONN_TIMEOUT", &cfg.Proxy.IdleConnTimeout},
		{"MUX_PROXY_EXPECT_CONTINUE_TIMEOUT", &cfg.Proxy.ExpectContinueTimeout},
		{"MUX_PROXY_TUNNEL_IDLE_TIMEOUT", &cfg.Proxy.TunnelIdleTimeout},
		{"MUX_PROXY_FAILOVER_COOLDOWN", &cfg.Proxy.FailoverCooldown},
		{"MUX_AUTH_FAILURE_WINDOW", &cfg.Auth.FailureWindow},
		{"MUX_AUTH_BLOCK_DURATION", &cfg.Auth.BlockDuration},
	}
	for _, entry := range durations {
		known[entry.name] = true
		if value, ok := values[entry.name]; ok {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("parse %s as duration: %w", entry.name, err)
			}
			*entry.dest = Duration(parsed)
		}
	}
	if countText, ok := values["MUX_UPSTREAM_COUNT"]; ok {
		count, err := strconv.Atoi(countText)
		if err != nil || count < 0 || count > 1024 {
			return fmt.Errorf("MUX_UPSTREAM_COUNT must be an integer between 0 and 1024")
		}
		upstreams := make([]UpstreamConfig, count)
		for index := range upstreams {
			prefix := fmt.Sprintf("MUX_UPSTREAM_%d_", index+1)
			names := []struct {
				suffix string
				dest   *string
			}{
				{"URL", &upstreams[index].URL},
				{"AUTH_USERNAME", &upstreams[index].Auth.Username},
				{"AUTH_PASSWORD", &upstreams[index].Auth.Password},
				{"TLS_CA_FILE", &upstreams[index].TLSCAFile},
			}
			for _, entry := range names {
				name := prefix + entry.suffix
				known[name] = true
				if value, exists := values[name]; exists {
					*entry.dest = value
				}
			}
			authName := prefix + "AUTH_ENABLED"
			known[authName] = true
			if value, exists := values[authName]; exists {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return fmt.Errorf("parse %s as boolean: %w", authName, err)
				}
				upstreams[index].Auth.Enabled = parsed
			}
			if upstreams[index].URL == "" {
				return fmt.Errorf("%sURL must be set", prefix)
			}
		}
		cfg.Upstreams = upstreams
	}
	for name := range values {
		if !known[name] {
			if strings.HasPrefix(name, "MUX_UPSTREAM_") {
				return fmt.Errorf("unknown %s; set MUX_UPSTREAM_COUNT and use indices from 1 to that count", name)
			}
			return fmt.Errorf("unknown configuration variable %s", name)
		}
	}
	return nil
}
