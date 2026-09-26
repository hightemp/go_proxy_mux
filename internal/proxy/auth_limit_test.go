package proxy

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func TestAuthFailureLimiter(t *testing.T) {
	cfg := config.Default().Auth
	cfg.MaxFailedAttempts = 3
	cfg.FailureWindow = config.Duration(time.Minute)
	cfg.BlockDuration = config.Duration(2 * time.Minute)
	limiter := newAuthFailureLimiter(cfg)
	now := time.Unix(100, 0)
	limiter.now = func() time.Time { return now }
	for range 2 {
		if got := limiter.attempt("192.0.2.1", false); got != 0 {
			t.Fatalf("early block = %s", got)
		}
	}
	if got := limiter.attempt("192.0.2.2", true); got != 0 {
		t.Fatalf("other IP blocked for %s", got)
	}
	if got := limiter.attempt("192.0.2.1", false); got != 2*time.Minute {
		t.Fatalf("block duration = %s", got)
	}
	now = now.Add(30 * time.Second)
	if got := limiter.attempt("192.0.2.1", true); got != 90*time.Second {
		t.Fatalf("valid credentials during block = %s", got)
	}
	now = now.Add(90 * time.Second)
	if got := limiter.attempt("192.0.2.1", true); got != 0 {
		t.Fatalf("valid credentials after block = %s", got)
	}
	if got := limiter.attempt("192.0.2.1", false); got != 0 {
		t.Fatalf("success did not reset failures: %s", got)
	}
	now = now.Add(time.Minute)
	if got := limiter.attempt("192.0.2.1", false); got != 0 {
		t.Fatalf("failure window did not reset: %s", got)
	}
}

func TestAuthFailureLimiterConcurrentAndBounded(t *testing.T) {
	cfg := config.Default().Auth
	cfg.MaxFailedAttempts = 10
	cfg.MaxTrackedIPs = 4
	limiter := newAuthFailureLimiter(cfg)
	var allowed sync.WaitGroup
	results := make(chan time.Duration, 32)
	for range 32 {
		allowed.Go(func() { results <- limiter.attempt("192.0.2.1", false) })
	}
	allowed.Wait()
	close(results)
	var unblocked int
	for duration := range results {
		if duration == 0 {
			unblocked++
		}
	}
	if unblocked != 9 {
		t.Fatalf("unblocked failures = %d, want 9", unblocked)
	}
	for i := range 12 {
		limiter.attempt("192.0.2."+strconv.Itoa(i+2), false)
	}
	if len(limiter.byIP) != cfg.MaxTrackedIPs {
		t.Fatalf("tracked IPs = %d, want %d", len(limiter.byIP), cfg.MaxTrackedIPs)
	}
}

func TestAuthFailureLimiterEvictsLeastRecentlyUsedIP(t *testing.T) {
	cfg := config.Default().Auth
	cfg.MaxTrackedIPs = 2
	limiter := newAuthFailureLimiter(cfg)
	limiter.attempt("192.0.2.1", false)
	limiter.attempt("192.0.2.2", false)
	limiter.attempt("192.0.2.1", false)
	limiter.attempt("192.0.2.3", false)
	if limiter.byIP["192.0.2.1"] == nil || limiter.byIP["192.0.2.2"] != nil || limiter.byIP["192.0.2.3"] == nil {
		t.Fatalf("unexpected tracked IPs after eviction: %v", limiter.byIP)
	}
}

func TestProxyAuthenticationRateLimit(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodConnect} {
		t.Run(method, func(t *testing.T) {
			cfg := config.Default()
			cfg.Auth.Enabled = true
			cfg.Auth.Username = "user"
			cfg.Auth.Password = "strong-password"
			cfg.Auth.MaxFailedAttempts = 3
			cfg.Auth.BlockDuration = config.Duration(2 * time.Minute)
			cfg.Upstreams = []config.UpstreamConfig{{URL: "http://127.0.0.1:1"}}
			server, err := NewProxyServer(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			for attempt, want := range []int{http.StatusProxyAuthRequired, http.StatusProxyAuthRequired, http.StatusTooManyRequests, http.StatusTooManyRequests} {
				request := httptest.NewRequest(method, "http://example.test/path", nil)
				request.RemoteAddr = "192.0.2.1:1234"
				request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:wrong")))
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				if response.Code != want {
					t.Fatalf("attempt %d: status = %d, want %d", attempt+1, response.Code, want)
				}
				if want == http.StatusTooManyRequests {
					retry, parseErr := strconv.Atoi(response.Header().Get("Retry-After"))
					if parseErr != nil || retry < 1 || retry > 120 {
						t.Fatalf("attempt %d: Retry-After = %q", attempt+1, response.Header().Get("Retry-After"))
					}
				}
			}
			request := httptest.NewRequest(method, "http://example.test/path", nil)
			request.RemoteAddr = "192.0.2.1:1234"
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:strong-password")))
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("valid credentials during block: status = %d", response.Code)
			}
		})
	}
}
