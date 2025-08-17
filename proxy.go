package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ProxyServer struct {
	config   *Config
	balancer LoadBalancer
}

func NewProxyServer(config *Config) *ProxyServer {
	balancer := CreateBalancer(config.Proxy.Algorithm, config.Upstreams)
	return &ProxyServer{
		config:   config,
		balancer: balancer,
	}
}

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

func (ps *ProxyServer) isAuthorized(r *http.Request) bool {
	auth := r.Header.Get("Proxy-Authorization")
	if auth == "" {
		return false
	}

	if !strings.HasPrefix(auth, "Basic ") {
		return false
	}

	encoded := auth[6:]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}

	credentials := string(decoded)
	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) != 2 {
		return false
	}

	username, password := parts[0], parts[1]
	return username == ps.config.Auth.Username && password == ps.config.Auth.Password
}

func (ps *ProxyServer) forwardRequest(w http.ResponseWriter, r *http.Request, upstream *UpstreamConfig) {
	proxyURL, err := url.Parse(upstream.URL)
	if err != nil {
		http.Error(w, "Invalid upstream URL", http.StatusBadGateway)
		return
	}

	client := &http.Client{
		Timeout: time.Duration(ps.config.Proxy.Timeout) * time.Second,
	}

	req, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	if upstream.Auth.Enabled {
		auth := base64.StdEncoding.EncodeToString([]byte(upstream.Auth.Username + ":" + upstream.Auth.Password))
		req.Header.Set("Proxy-Authorization", "Basic "+auth)
	}

	req.Header.Set("X-Forwarded-For", r.RemoteAddr)
	req.URL.Scheme = proxyURL.Scheme
	req.URL.Host = proxyURL.Host

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error forwarding request to %s: %v", upstream.URL, err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request, upstream *UpstreamConfig) {
	proxyURL, err := url.Parse(upstream.URL)
	if err != nil {
		http.Error(w, "Invalid upstream URL", http.StatusBadGateway)
		return
	}

	proxyConn, err := net.DialTimeout("tcp", proxyURL.Host, time.Duration(ps.config.Proxy.Timeout)*time.Second)
	if err != nil {
		log.Printf("Error connecting to upstream proxy %s: %v", upstream.URL, err)
		http.Error(w, "Failed to connect to upstream proxy", http.StatusBadGateway)
		return
	}
	defer proxyConn.Close()

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", r.URL.Host, r.URL.Host)

	if upstream.Auth.Enabled {
		auth := base64.StdEncoding.EncodeToString([]byte(upstream.Auth.Username + ":" + upstream.Auth.Password))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
	}

	connectReq += "\r\n"

	_, err = proxyConn.Write([]byte(connectReq))
	if err != nil {
		http.Error(w, "Failed to send CONNECT request", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	go io.Copy(proxyConn, clientConn)
	io.Copy(clientConn, proxyConn)
}

func (ps *ProxyServer) Start() error {
	addr := fmt.Sprintf("%s:%d", ps.config.Server.Host, ps.config.Server.Port)
	log.Printf("Starting proxy server on %s", addr)
	return http.ListenAndServe(addr, ps)
}
