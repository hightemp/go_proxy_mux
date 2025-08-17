package main

import (
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Auth      AuthConfig       `yaml:"auth"`
	Proxy     ProxyConfig      `yaml:"proxy"`
	Upstreams []UpstreamConfig `yaml:"upstreams"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ProxyConfig struct {
	Algorithm string `yaml:"algorithm"`
	Timeout   int    `yaml:"timeout"`
}

type UpstreamConfig struct {
	URL  string       `yaml:"url"`
	Auth UpstreamAuth `yaml:"auth"`
}

type UpstreamAuth struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

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
