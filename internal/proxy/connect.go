package proxy

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

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request, upstream *config.UpstreamConfig) {
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

	response := make([]byte, 4096)
	n, err := proxyConn.Read(response)
	if err != nil {
		http.Error(w, "Failed to read proxy response", http.StatusBadGateway)
		return
	}

	responseStr := string(response[:n])
	if !strings.Contains(responseStr, "200") {
		log.Printf("Upstream proxy returned error: %s", responseStr)
		http.Error(w, "Upstream proxy connection failed", http.StatusBadGateway)
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
