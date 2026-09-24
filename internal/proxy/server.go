// Package proxy serves HTTP proxy requests through configured upstreams.
package proxy

import (
	"fmt"
	"log"
	"net/http"

	"github.com/hightemp/go_proxy_mux/internal/balancer"
	"github.com/hightemp/go_proxy_mux/internal/config"
)

// ProxyServer authenticates and forwards HTTP and CONNECT requests.
type ProxyServer struct {
	config   *config.Config
	balancer balancer.LoadBalancer
}

// NewProxyServer creates a proxy handler from the loaded configuration.
func NewProxyServer(cfg *config.Config) *ProxyServer {
	selector := balancer.CreateBalancer(cfg.Proxy.Algorithm, cfg.Upstreams)
	return &ProxyServer{
		config:   cfg,
		balancer: selector,
	}
}

// ServeHTTP authenticates and dispatches a proxy request.
func (ps *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ps.config.Auth.Enabled && !ps.isAuthorized(r) {
		w.Header().Set("Proxy-Authenticate", "Basic realm=\"Proxy\"")
		http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
		return
	}

	upstream := ps.balancer.Next()
	if upstream == nil {
		http.Error(w, "No upstream servers available", http.StatusBadGateway)
		return
	}

	if r.Method == "CONNECT" {
		ps.handleConnect(w, r, upstream)
	} else {
		ps.forwardRequest(w, r, upstream)
	}
}

// Start listens for HTTP proxy requests until the listener fails.
func (ps *ProxyServer) Start() error {
	addr := fmt.Sprintf("%s:%d", ps.config.Server.Host, ps.config.Server.Port)
	log.Printf("Starting proxy server on %s", addr)
	return http.ListenAndServe(addr, ps)
}
