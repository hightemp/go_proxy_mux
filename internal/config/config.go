// Package config loads proxy settings from YAML.
package config

import (
	"os"

	"gopkg.in/yaml.v2"
)

// Config contains all server, authentication, and upstream settings.
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Auth      AuthConfig       `yaml:"auth"`
	Proxy     ProxyConfig      `yaml:"proxy"`
	Upstreams []UpstreamConfig `yaml:"upstreams"`
}

// ServerConfig selects the listening address.
type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// AuthConfig contains credentials accepted from clients.
type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ProxyConfig controls upstream selection and timeouts.
type ProxyConfig struct {
	Algorithm string `yaml:"algorithm"`
	Timeout   int    `yaml:"timeout"`
}

// UpstreamConfig describes one proxy server.
type UpstreamConfig struct {
	URL  string       `yaml:"url"`
	Auth UpstreamAuth `yaml:"auth"`
}

// UpstreamAuth contains credentials sent to an upstream proxy.
type UpstreamAuth struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// LoadConfig reads a YAML configuration file.
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}
