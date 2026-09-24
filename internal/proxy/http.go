package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
)

import "github.com/hightemp/go_proxy_mux/internal/balancer"

func (ps *ProxyServer) forwardRequest(w http.ResponseWriter, r *http.Request, selected balancer.Selection) {
	target := *r.URL
	if target.Scheme == "" && target.Host == "" && r.Host != "" {
		target.Scheme = "http"
		target.Host = r.Host
	}
	if (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		http.Error(w, "Invalid request destination", http.StatusBadRequest)
		return
	}
	canRetry := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		r.ContentLength == 0 && (r.Body == nil || r.Body == http.NoBody)
	for attempt := 0; attempt < 2; attempt++ {
		request := r.Clone(r.Context())
		request.URL = cloneURL(&target)
		request.RequestURI = ""
		request.Host = target.Host
		request.Header = r.Header.Clone()
		removeHopHeaders(request.Header)
		request.Header.Del("X-Forwarded-For")

		response, err := ps.clients[selected.Index].Do(request)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			ps.balancer.MarkFailure(selected.Index)
			log.Printf("Upstream %d HTTP request failed: %v", selected.Index, sanitizeError(err))
			if canRetry && attempt == 0 {
				if alternate, ok := ps.balancer.Next(selected.Index); ok {
					selected = alternate
					continue
				}
			}
			writeUpstreamError(w, err)
			return
		}
		if response.StatusCode == http.StatusProxyAuthRequired {
			_ = response.Body.Close()
			ps.balancer.MarkFailure(selected.Index)
			if canRetry && attempt == 0 {
				if alternate, ok := ps.balancer.Next(selected.Index); ok {
					selected = alternate
					continue
				}
			}
			http.Error(w, "Upstream proxy authentication failed", http.StatusBadGateway)
			return
		}
		ps.balancer.MarkSuccess(selected.Index)
		headers := response.Header.Clone()
		removeHopHeaders(headers)
		for name, values := range headers {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if _, copyErr := io.Copy(w, response.Body); copyErr != nil {
			log.Printf("HTTP response copy failed: %v", sanitizeError(copyErr))
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			log.Printf("HTTP response close failed: %v", sanitizeError(closeErr))
		}
		return
	}
}

func cloneURL(value *url.URL) *url.URL {
	copy := *value
	return &copy
}

func writeUpstreamError(w http.ResponseWriter, err error) {
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) {
		http.Error(w, "Upstream proxy timed out", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "Upstream proxy failed", http.StatusBadGateway)
}

func sanitizeError(err error) string {
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) {
		return "network timeout"
	}
	return "network error"
}
