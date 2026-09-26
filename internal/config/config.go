// Package config loads and validates proxy settings from YAML.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

// Duration is a time.Duration represented by a Go duration string in YAML.
type Duration time.Duration

// UnmarshalYAML parses a duration such as 15s or 2m.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var value string
	if err := unmarshal(&value); err != nil {
		return fmt.Errorf("duration must be a string such as 15s: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value, err)
	}
	*d = Duration(parsed)
	return nil
}

// Config contains all server, authentication, and upstream settings.
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Auth      AuthConfig       `yaml:"auth"`
	Proxy     ProxyConfig      `yaml:"proxy"`
	Upstreams []UpstreamConfig `yaml:"upstreams"`
}

// ServerConfig selects the listening address and connection limits.
type ServerConfig struct {
	Port                            int       `yaml:"port"`
	Host                            string    `yaml:"host"`
	TLS                             TLSConfig `yaml:"tls"`
	AllowInsecurePublicHTTP         bool      `yaml:"allow_insecure_public_http"`
	AllowUnauthenticatedPublicProxy bool      `yaml:"allow_unauthenticated_public_proxy"`
	MaxConnections                  int       `yaml:"max_connections"`
	MaxConnectionsPerIP             int       `yaml:"max_connections_per_ip"`
	MaxHeaderBytes                  int       `yaml:"max_header_bytes"`
	HTTP2MaxConcurrentStreams       int       `yaml:"http2_max_concurrent_streams"`
	HTTP2SendPingTimeout            Duration  `yaml:"http2_send_ping_timeout"`
	HTTP2PingTimeout                Duration  `yaml:"http2_ping_timeout"`
	HTTP2WriteByteTimeout           Duration  `yaml:"http2_write_byte_timeout"`
	ReadHeaderTimeout               Duration  `yaml:"read_header_timeout"`
	IdleTimeout                     Duration  `yaml:"idle_timeout"`
	ShutdownTimeout                 Duration  `yaml:"shutdown_timeout"`
}

// TLSConfig names the certificate and key used by the listener.
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// AuthConfig contains credentials accepted from clients.
type AuthConfig struct {
	Enabled           bool     `yaml:"enabled"`
	Username          string   `yaml:"username"`
	Password          string   `yaml:"password"`
	MaxFailedAttempts int      `yaml:"max_failed_attempts"`
	FailureWindow     Duration `yaml:"failure_window"`
	BlockDuration     Duration `yaml:"block_duration"`
	MaxTrackedIPs     int      `yaml:"max_tracked_ips"`
}

// ProxyConfig controls upstream selection, timeouts, and tunnel limits.
type ProxyConfig struct {
	Algorithm               string   `yaml:"algorithm"`
	Timeout                 int      `yaml:"timeout"` // Legacy value in seconds.
	Network                 string   `yaml:"network"`
	DialTimeout             Duration `yaml:"dial_timeout"`
	DialKeepAlive           Duration `yaml:"dial_keep_alive"`
	TLSHandshakeTimeout     Duration `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout   Duration `yaml:"response_header_timeout"`
	ResponseBodyIdleTimeout Duration `yaml:"response_body_idle_timeout"`
	IdleConnTimeout         Duration `yaml:"idle_conn_timeout"`
	ExpectContinueTimeout   Duration `yaml:"expect_continue_timeout"`
	MaxIdleConns            int      `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost     int      `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost         int      `yaml:"max_conns_per_host"`
	MaxTunnels              int      `yaml:"max_tunnels"`
	MaxTunnelsPerIP         int      `yaml:"max_tunnels_per_ip"`
	TunnelIdleTimeout       Duration `yaml:"tunnel_idle_timeout"`
	FailoverCooldown        Duration `yaml:"failover_cooldown"`
}

// UpstreamConfig describes one proxy server.
type UpstreamConfig struct {
	URL       string       `yaml:"url"`
	Auth      UpstreamAuth `yaml:"auth"`
	TLSCAFile string       `yaml:"tls_ca_file"`
}

// UpstreamAuth contains credentials sent to an upstream proxy.
type UpstreamAuth struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Default returns backward-compatible settings with bounded resource use.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:                  "127.0.0.1",
			Port:                  8380,
			MaxConnections:        1024,
			MaxConnectionsPerIP:   128,
			MaxHeaderBytes:        64 << 10,
			HTTP2SendPingTimeout:  Duration(time.Minute),
			HTTP2PingTimeout:      Duration(15 * time.Second),
			HTTP2WriteByteTimeout: Duration(30 * time.Second),
			ReadHeaderTimeout:     Duration(15 * time.Second),
			IdleTimeout:           Duration(2 * time.Minute),
			ShutdownTimeout:       Duration(15 * time.Second),
		},
		Auth: AuthConfig{
			MaxFailedAttempts: 10,
			FailureWindow:     Duration(time.Minute),
			BlockDuration:     Duration(5 * time.Minute),
			MaxTrackedIPs:     4096,
		},
		Proxy: ProxyConfig{
			Algorithm:               "roundrobin",
			Timeout:                 30,
			Network:                 "auto",
			DialTimeout:             Duration(10 * time.Second),
			DialKeepAlive:           Duration(30 * time.Second),
			TLSHandshakeTimeout:     Duration(10 * time.Second),
			ResponseHeaderTimeout:   Duration(30 * time.Second),
			ResponseBodyIdleTimeout: Duration(2 * time.Minute),
			IdleConnTimeout:         Duration(90 * time.Second),
			ExpectContinueTimeout:   Duration(time.Second),
			MaxIdleConns:            100,
			MaxIdleConnsPerHost:     10,
			MaxTunnels:              256,
			MaxTunnelsPerIP:         128,
			TunnelIdleTimeout:       Duration(2 * time.Minute),
			FailoverCooldown:        Duration(30 * time.Second),
		},
	}
}

// LoadConfig reads YAML and process environment overrides.
func LoadConfig(filename string) (*Config, error) {
	return LoadConfigWithEnv(filename, "")
}

// LoadConfigWithEnv merges YAML, an optional .env file, and process environment.
// Process environment takes precedence over .env, then YAML, then defaults.
func LoadConfigWithEnv(filename, envFile string) (*Config, error) {
	config := Default()
	var fileErr error
	if filename != "" {
		data, err := os.ReadFile(filename)
		if err == nil {
			if err := yaml.UnmarshalStrict(data, &config); err != nil {
				return nil, fmt.Errorf("parse config: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		} else {
			fileErr = err
		}
	}
	values, err := readEnvValues(envFile)
	if err != nil {
		return nil, err
	}
	if fileErr != nil && len(values) == 0 {
		return nil, fmt.Errorf("read config: %w", fileErr)
	}
	if err := applyEnvOverrides(&config, values); err != nil {
		return nil, err
	}
	if err := Validate(&config); err != nil {
		return nil, err
	}
	return &config, nil
}
