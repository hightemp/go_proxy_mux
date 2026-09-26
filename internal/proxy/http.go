package proxy

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/balancer"
	"github.com/hightemp/go_proxy_mux/internal/socks"
)

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

		response, cancel, err := ps.doHTTPRequest(request, selected.Index)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			if !errors.Is(err, socks.ErrUnsupportedDestination) {
				ps.balancer.MarkFailure(selected.Index)
			}
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
			cancel(nil)
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
		defer cancel(nil)
		defer func() {
			if closeErr := response.Body.Close(); closeErr != nil {
				log.Printf("HTTP response close failed: %v", sanitizeError(closeErr))
			}
		}()
		ps.balancer.MarkSuccess(selected.Index)
		headers := response.Header.Clone()
		removeHopHeaders(headers)
		for name, values := range headers {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if copyErr := copyHTTPResponse(w, response, time.Duration(ps.config.Proxy.ResponseBodyIdleTimeout), cancel); copyErr != nil {
			log.Printf("HTTP response copy failed: %v", sanitizeError(copyErr))
			// Headers are already sent; abort so a truncated chunked body cannot look complete.
			panic(http.ErrAbortHandler)
		}
		return
	}
}

// doHTTPRequest limits the request setup through response headers, then leaves
// the response body governed by its own idle timeout and the client context.
func (ps *ProxyServer) doHTTPRequest(request *http.Request, index int) (*http.Response, context.CancelCauseFunc, error) {
	ctx, cancel := context.WithCancelCause(request.Context())
	request = request.WithContext(ctx)
	var setupState atomic.Int32 // 0: waiting, 1: headers received, 2: timed out
	timer := time.AfterFunc(time.Duration(ps.config.Proxy.Timeout)*time.Second, func() {
		if setupState.CompareAndSwap(0, 2) {
			cancel(context.DeadlineExceeded)
		}
	})
	response, err := ps.clients[index].Do(request)
	completed := setupState.CompareAndSwap(0, 1)
	timer.Stop()
	if !completed {
		if response != nil {
			_ = response.Body.Close()
		}
		cancel(context.DeadlineExceeded)
		return nil, nil, context.DeadlineExceeded
	}
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		cancel(nil)
		return nil, nil, err
	}
	return response, cancel, nil
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
	if errors.Is(err, errResponseBodyIdle) {
		return "response body idle timeout"
	}
	var networkErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkErr) && networkErr.Timeout()) {
		return "network timeout"
	}
	return "network error"
}
