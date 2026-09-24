// Package proxy serves HTTP and CONNECT requests through configured upstreams.
package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/balancer"
	"github.com/hightemp/go_proxy_mux/internal/config"
	"github.com/hightemp/go_proxy_mux/internal/socks"
)

// ProxyServer authenticates clients and forwards requests to upstream proxies.
type ProxyServer struct {
	config      *config.Config
	balancer    *balancer.Balancer
	clients     []*http.Client
	transports  []*http.Transport
	upstreamTLS []*tls.Config
	tunnels     *tunnelRegistry
	tunnelSlots chan struct{}
}

// NewProxyServer creates a handler and dedicated transport for each upstream.
func NewProxyServer(cfg *config.Config) (*ProxyServer, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, err
	}
	ps := &ProxyServer{
		config:      cfg,
		balancer:    balancer.New(cfg.Proxy, cfg.Upstreams),
		tunnels:     newTunnelRegistry(),
		tunnelSlots: make(chan struct{}, cfg.Proxy.MaxTunnels),
	}
	timeout := time.Duration(cfg.Proxy.Timeout) * time.Second
	for i, upstream := range cfg.Upstreams {
		proxyURL, err := config.UpstreamURL(upstream)
		if err != nil {
			return nil, fmt.Errorf("upstreams[%d]: %w", i, err)
		}
		var tlsConfig *tls.Config
		if upstream.TLSCAFile != "" {
			roots, err := x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("load system roots for upstreams[%d]: %w", i, err)
			}
			if roots == nil {
				roots = x509.NewCertPool()
			}
			caData, err := os.ReadFile(upstream.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("read upstreams[%d].tls_ca_file: %w", i, err)
			}
			if !roots.AppendCertsFromPEM(caData) {
				return nil, fmt.Errorf("upstreams[%d].tls_ca_file contains no PEM certificates", i)
			}
			tlsConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		}
		transport := &http.Transport{
			DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSClientConfig:       tlsConfig,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}
		if proxyURL.Scheme == "socks4" || proxyURL.Scheme == "socks5" {
			scheme, address := proxyURL.Scheme, proxyURL.Host
			credentials := socks.Credentials{
				Enabled:  upstream.Auth.Enabled,
				Username: upstream.Auth.Username,
				Password: upstream.Auth.Password,
			}
			transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
				if network != "tcp" && network != "tcp4" && network != "tcp6" {
					return nil, fmt.Errorf("SOCKS upstream supports TCP only")
				}
				return socks.DialContext(ctx, scheme, address, target, credentials, timeout)
			}
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		ps.transports = append(ps.transports, transport)
		ps.upstreamTLS = append(ps.upstreamTLS, tlsConfig)
		ps.clients = append(ps.clients, &http.Client{
			Transport:     transport,
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		})
	}
	return ps, nil
}

// ServeHTTP authenticates and dispatches one proxy request.
func (ps *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ps.config.Auth.Enabled && !ps.isAuthorized(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Proxy"`)
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}
	selected, ok := ps.balancer.Next(-1)
	if !ok {
		http.Error(w, "No upstream servers available", http.StatusBadGateway)
		return
	}
	if r.Method == http.MethodConnect {
		ps.handleConnect(w, r, selected)
	} else {
		ps.forwardRequest(w, r, selected)
	}
}

func (ps *ProxyServer) closeIdleConnections() {
	for _, transport := range ps.transports {
		transport.CloseIdleConnections()
	}
}
