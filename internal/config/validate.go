package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Validate checks every setting needed before a listener is opened.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	host := cfg.Server.Host
	if host == "" || strings.ContainsAny(host, " /\r\n\t") {
		return fmt.Errorf("server.host must be a listening hostname or IP address")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if cfg.Server.MaxConnections < 1 {
		return fmt.Errorf("server.max_connections must be positive")
	}
	if cfg.Server.ReadHeaderTimeout <= 0 || cfg.Server.IdleTimeout <= 0 || cfg.Server.ShutdownTimeout <= 0 {
		return fmt.Errorf("server timeouts must be positive")
	}
	certFile, keyFile := cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("server.tls.cert_file and server.tls.key_file must be set together")
	}
	if certFile != "" {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("load server TLS certificate and key: %w", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse server TLS certificate: %w", err)
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
			return fmt.Errorf("server TLS certificate is not currently valid")
		}
	}
	ip := net.ParseIP(host)
	local := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if !local && certFile == "" && !cfg.Server.AllowInsecurePublicHTTP {
		return fmt.Errorf("public HTTP listener requires server.allow_insecure_public_http or server.tls certificate and key")
	}
	if !local && !cfg.Auth.Enabled && !cfg.Server.AllowUnauthenticatedPublicProxy {
		return fmt.Errorf("public listener requires auth.enabled or server.allow_unauthenticated_public_proxy")
	}
	if cfg.Auth.Enabled {
		if cfg.Auth.Username == "" || cfg.Auth.Password == "" || strings.Contains(cfg.Auth.Username, ":") {
			return fmt.Errorf("auth.enabled requires a username without ':' and a password")
		}
		if cfg.Auth.Username == "<set-username>" || cfg.Auth.Password == "<set-strong-password>" || (cfg.Auth.Username == "admin" && cfg.Auth.Password == "password") {
			return fmt.Errorf("replace demonstration auth.username and auth.password before starting")
		}
	}
	if cfg.Proxy.Algorithm != "roundrobin" && cfg.Proxy.Algorithm != "random" {
		return fmt.Errorf("proxy.algorithm must be roundrobin or random")
	}
	if cfg.Proxy.Timeout < 1 || cfg.Proxy.MaxTunnels < 1 || cfg.Proxy.TunnelIdleTimeout <= 0 || cfg.Proxy.FailoverCooldown <= 0 {
		return fmt.Errorf("proxy timeout and limits must be positive")
	}
	if len(cfg.Upstreams) == 0 {
		return fmt.Errorf("upstreams must contain at least one proxy")
	}
	for i, up := range cfg.Upstreams {
		parsed, err := UpstreamURL(up)
		if err != nil {
			return fmt.Errorf("upstreams[%d]: %w", i, err)
		}
		if up.Auth.Enabled {
			if up.Auth.Username == "" || up.Auth.Username == "<upstream-username>" || up.Auth.Password == "<upstream-password>" {
				return fmt.Errorf("upstreams[%d].auth contains empty or demonstration credentials", i)
			}
			switch parsed.Scheme {
			case "socks4":
				if up.Auth.Password != "" || len(up.Auth.Username) > 255 || strings.ContainsRune(up.Auth.Username, 0) {
					return fmt.Errorf("upstreams[%d].auth: SOCKS4 accepts a USERID of at most 255 bytes and no password", i)
				}
			case "socks5":
				if up.Auth.Password == "" || len(up.Auth.Username) > 255 || len(up.Auth.Password) > 255 {
					return fmt.Errorf("upstreams[%d].auth: SOCKS5 requires a username and password of 1 to 255 bytes", i)
				}
			default:
				if up.Auth.Password == "" || strings.Contains(up.Auth.Username, ":") {
					return fmt.Errorf("upstreams[%d].auth requires a username without ':' and a password", i)
				}
			}
		}
		if up.TLSCAFile != "" {
			if parsed.Scheme != "https" {
				return fmt.Errorf("upstreams[%d].tls_ca_file requires an HTTPS upstream", i)
			}
			data, err := os.ReadFile(up.TLSCAFile)
			if err != nil {
				return fmt.Errorf("read upstreams[%d].tls_ca_file: %w", i, err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(data) {
				return fmt.Errorf("upstreams[%d].tls_ca_file contains no PEM certificates", i)
			}
		}
	}
	return nil
}

// UpstreamURL parses and normalizes a configured proxy URL.
func UpstreamURL(up UpstreamConfig) (*url.URL, error) {
	parsed, err := url.Parse(up.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL syntax")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks4" && parsed.Scheme != "socks5" {
		return nil, fmt.Errorf("upstream URL scheme must be http, https, socks4, or socks5")
	}
	if parsed.Hostname() == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("upstream URL must contain only scheme and host[:port], without credentials")
	}
	if strings.HasSuffix(parsed.Hostname(), ".example.com") {
		return nil, fmt.Errorf("replace demonstration upstream URL")
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			port = "1080"
		}
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return nil, fmt.Errorf("upstream URL port must be between 1 and 65535")
	}
	parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
	parsed.Path = ""
	if up.Auth.Enabled && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		parsed.User = url.UserPassword(up.Auth.Username, up.Auth.Password)
	}
	return parsed, nil
}
