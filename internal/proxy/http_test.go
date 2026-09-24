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
	defer response.Body.Close()
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
