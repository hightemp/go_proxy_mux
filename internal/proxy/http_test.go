package proxy

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func newTestProxy(t *testing.T, cfg *config.Config) *httptest.Server {
	t.Helper()
	handler, err := NewProxyServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func clientThroughProxy(t *testing.T, proxyAddress string, username, password string) *http.Client {
	t.Helper()
	parsed, err := url.Parse(proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	if username != "" {
		parsed.User = url.UserPassword(username, password)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(parsed)}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestHTTPForwardingAndAuthentication(t *testing.T) {
	observed := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Clone(r.Context())
		w.Header().Set("Location", "http://example.test/next")
		w.Header().Set("Connection", "X-Remove")
		w.Header().Add("Connection", "X-Remove-2")
		w.Header().Set("X-Remove", "response-hop")
		w.Header().Set("X-Remove-2", "response-hop-2")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Auth = config.AuthConfig{Enabled: true, Username: "client-user", Password: "strong-client-password"}
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL, Auth: config.UpstreamAuth{Enabled: true, Username: "up-user", Password: "up-password"}}}
	proxyServer := newTestProxy(t, &cfg)
	client := clientThroughProxy(t, proxyServer.URL, cfg.Auth.Username, cfg.Auth.Password)
	request, err := http.NewRequest(http.MethodGet, "http://example.test/path?q=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Connection", "X-Remove")
	request.Header.Add("Connection", "X-Remove-2")
	request.Header.Set("X-Remove", "secret-hop")
	request.Header.Set("X-Remove-2", "secret-hop-2")
	request.Header.Set("X-Forwarded-For", "forged")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.StatusCode)
	}
	if response.Header.Get("X-Remove") != "" || response.Header.Get("X-Remove-2") != "" {
		t.Fatal("upstream hop header leaked to client")
	}
	received := <-observed
	if received.RequestURI != "http://example.test/path?q=1" || received.Host != "example.test" {
		t.Fatalf("upstream request = %q, Host = %q", received.RequestURI, received.Host)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("up-user:up-password"))
	if got := received.Header.Get("Proxy-Authorization"); got != wantAuth {
		t.Fatalf("upstream auth differs from configured credentials: present=%v", got != "")
	}
	if received.Header.Get("X-Remove") != "" || received.Header.Get("X-Remove-2") != "" || received.Header.Get("X-Forwarded-For") != "" {
		t.Fatal("hop/client forwarding headers leaked")
	}
	noAuthCfg := cfg
	noAuthCfg.Upstreams = append([]config.UpstreamConfig(nil), cfg.Upstreams...)
	noAuthCfg.Upstreams[0].Auth.Enabled = false
	noUpstreamAuthProxy := newTestProxy(t, &noAuthCfg)
	noUpstreamAuthClient := clientThroughProxy(t, noUpstreamAuthProxy.URL, cfg.Auth.Username, cfg.Auth.Password)
	withoutAuth, err := noUpstreamAuthClient.Get("http://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	_ = withoutAuth.Body.Close()
	if got := (<-observed).Header.Get("Proxy-Authorization"); got != "" {
		t.Fatal("client Proxy-Authorization leaked to upstream without upstream auth")
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestRetryOnlyBodylessSafeHTTP(t *testing.T) {
	for _, tt := range []struct {
		name, method string
		body         io.Reader
		wantStatus   int
		wantHits     int
	}{
		{"get", http.MethodGet, nil, http.StatusOK, 1},
		{"post", http.MethodPost, strings.NewReader("payload"), http.StatusBadGateway, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			cfg := config.Default()
			cfg.Proxy.Timeout = 1
			cfg.Upstreams = []config.UpstreamConfig{{URL: "http://" + unusedAddress(t)}, {URL: upstream.URL}}
			proxyServer := newTestProxy(t, &cfg)
			client := clientThroughProxy(t, proxyServer.URL, "", "")
			request, err := http.NewRequest(tt.method, "http://example.test/path", tt.body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != tt.wantStatus || int(hits.Load()) != tt.wantHits {
				t.Fatalf("status=%d, good upstream hits=%d", response.StatusCode, hits.Load())
			}
		})
	}
}

func TestHTTPSUpstreamVerification(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "http://example.test/path" {
			t.Errorf("request URI = %q", r.RequestURI)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, ca string
		want     int
	}{
		{"trusted", certPath, http.StatusNoContent},
		{"untrusted", "", http.StatusBadGateway},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL, TLSCAFile: tt.ca}}
			proxyServer := newTestProxy(t, &cfg)
			client := clientThroughProxy(t, proxyServer.URL, "", "")
			response, err := client.Get("http://example.test/path")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != tt.want {
				t.Fatalf("status=%d, want %d", response.StatusCode, tt.want)
			}
		})
	}
}

func TestCancelledClientStopsUpstreamRequest(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	client := clientThroughProxy(t, proxyServer.URL, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream request continued after client cancellation")
	}
	if err := <-result; err == nil {
		t.Fatal("cancelled client request unexpectedly succeeded")
	}
}

func TestUpstream407BecomesBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="upstream"`)
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	client := clientThroughProxy(t, proxyServer.URL, "", "")
	response, err := client.Get("http://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || response.Header.Get("Proxy-Authenticate") != "" {
		t.Fatalf("response status = %d, upstream challenge present = %v", response.StatusCode, response.Header.Get("Proxy-Authenticate") != "")
	}
}

func TestResponseHeaderTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.Timeout = 5
	cfg.Proxy.ResponseHeaderTimeout = config.Duration(100 * time.Millisecond)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	start := time.Now()
	response, err := clientThroughProxy(t, proxyServer.URL, "", "").Get("http://example.test/slow")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusGatewayTimeout || time.Since(start) > time.Second {
		t.Fatalf("status = %d after %s", response.StatusCode, time.Since(start))
	}
}

func TestHTTPSetupTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.Timeout = 1
	cfg.Proxy.ResponseHeaderTimeout = config.Duration(3 * time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	start := time.Now()
	response, err := clientThroughProxy(t, proxyServer.URL, "", "").Get("http://example.test/slow-headers")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusGatewayTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("status = %d after %s, want 504 near 1s", response.StatusCode, time.Since(start))
	}
}

func TestLongHTTPResponseOutlivesSetupTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 6 {
			if _, err := io.WriteString(w, "chunk\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(250 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.Timeout = 1
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	client := clientThroughProxy(t, proxyServer.URL, "", "")
	client.Timeout = 4 * time.Second
	response, err := client.Get("http://example.test/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(data) != strings.Repeat("chunk\n", 6) {
		t.Fatalf("status = %d, body = %q", response.StatusCode, data)
	}
}

func TestStalledHTTPResponseIsAborted(t *testing.T) {
	upstreamStopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamStopped)
		if _, err := io.WriteString(w, "start"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.Timeout = 5
	cfg.Proxy.ResponseBodyIdleTimeout = config.Duration(150 * time.Millisecond)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	client := clientThroughProxy(t, proxyServer.URL, "", "")
	client.Timeout = 2 * time.Second
	response, err := client.Get("http://example.test/stalled-body")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	start := time.Now()
	_, err = io.ReadAll(response.Body)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("body read error = %v after %s, want prompt abort", err, time.Since(start))
	}
	select {
	case <-upstreamStopped:
	case <-time.After(time.Second):
		t.Fatal("stalled upstream request was not canceled")
	}
}

func TestCancelledClientStopsStreamingUpstream(t *testing.T) {
	upstreamStopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamStopped)
		if _, err := io.WriteString(w, "chunk"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.Timeout = 1
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	proxyServer := newTestProxy(t, &cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := clientThroughProxy(t, proxyServer.URL, "", "")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	first := make([]byte, len("chunk"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "chunk" {
		t.Fatalf("first chunk = %q, error = %v", first, err)
	}
	cancel()
	select {
	case <-upstreamStopped:
	case <-time.After(time.Second):
		t.Fatal("streaming upstream request continued after client cancellation")
	}
}

func TestUpstreamTLSHandshakeTimeout(t *testing.T) {
	address := startRawUpstream(t, func(conn net.Conn) {
		time.Sleep(300 * time.Millisecond)
	})
	cfg := config.Default()
	cfg.Proxy.Timeout = 5
	cfg.Proxy.TLSHandshakeTimeout = config.Duration(80 * time.Millisecond)
	cfg.Upstreams = []config.UpstreamConfig{{URL: strings.Replace(address, "http://", "https://", 1)}}
	proxyServer := newTestProxy(t, &cfg)
	response, err := clientThroughProxy(t, proxyServer.URL, "", "").Get("http://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", response.StatusCode)
	}
}

func TestOutboundNetworkFamily(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	for _, tt := range []struct {
		network string
		status  int
	}{
		{"tcp4", http.StatusNoContent},
		{"tcp6", http.StatusBadGateway},
	} {
		t.Run(tt.network, func(t *testing.T) {
			cfg := config.Default()
			cfg.Proxy.Network = tt.network
			cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
			proxyServer := newTestProxy(t, &cfg)
			response, err := clientThroughProxy(t, proxyServer.URL, "", "").Get("http://example.test/path")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
		})
	}
}

func TestTransportPoolSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Proxy.MaxIdleConns = 17
	cfg.Proxy.MaxIdleConnsPerHost = 4
	cfg.Proxy.MaxConnsPerHost = 9
	cfg.Proxy.IdleConnTimeout = config.Duration(11 * time.Second)
	cfg.Proxy.TLSHandshakeTimeout = config.Duration(12 * time.Second)
	cfg.Proxy.ResponseHeaderTimeout = config.Duration(13 * time.Second)
	cfg.Proxy.ExpectContinueTimeout = config.Duration(14 * time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: "http://127.0.0.1:8080"}}
	handler, err := NewProxyServer(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	transport := handler.transports[0]
	if transport.MaxIdleConns != 17 || transport.MaxIdleConnsPerHost != 4 || transport.MaxConnsPerHost != 9 || transport.IdleConnTimeout != 11*time.Second || transport.TLSHandshakeTimeout != 12*time.Second || transport.ResponseHeaderTimeout != 13*time.Second || transport.ExpectContinueTimeout != 14*time.Second {
		t.Fatal("HTTP transport settings were not applied")
	}
}
