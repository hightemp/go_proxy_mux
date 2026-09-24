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
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ProxyConfig controls upstream selection, timeouts, and tunnel limits.
type ProxyConfig struct {
	Algorithm         string   `yaml:"algorithm"`
	Timeout           int      `yaml:"timeout"` // Legacy value in seconds.
	MaxTunnels        int      `yaml:"max_tunnels"`
	TunnelIdleTimeout Duration `yaml:"tunnel_idle_timeout"`
	FailoverCooldown  Duration `yaml:"failover_cooldown"`
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
			Host:              "127.0.0.1",
			Port:              8380,
			MaxConnections:    1024,
			ReadHeaderTimeout: Duration(15 * time.Second),
			IdleTimeout:       Duration(2 * time.Minute),
			ShutdownTimeout:   Duration(15 * time.Second),
		},
		Proxy: ProxyConfig{
			Algorithm:         "roundrobin",
			Timeout:           30,
			MaxTunnels:        256,
			TunnelIdleTimeout: Duration(2 * time.Minute),
			FailoverCooldown:  Duration(30 * time.Second),
		},
	}
}

// LoadConfig reads a YAML configuration file and rejects invalid settings.
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	config := Default()
	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := Validate(&config); err != nil {
		return nil, err
	}
	return &config, nil
}
