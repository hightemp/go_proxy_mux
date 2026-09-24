package proxy

import (
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func (ps *ProxyServer) forwardRequest(w http.ResponseWriter, r *http.Request, upstream *config.UpstreamConfig) {
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
