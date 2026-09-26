package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func benchmarkProxyHandler(b *testing.B, upstreamURL string) *ProxyServer {
	b.Helper()
	cfg := config.Default()
	cfg.Auth.Enabled = true
	cfg.Auth.Username = "load-user"
	cfg.Auth.Password = "load-password"
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	handler, err := NewProxyServer(&cfg)
	if err != nil {
		b.Fatal(err)
	}
	return handler
}

func benchmarkProxy(b *testing.B, upstreamURL string) *httptest.Server {
	b.Helper()
	server := httptest.NewServer(benchmarkProxyHandler(b, upstreamURL))
	b.Cleanup(server.Close)
	return server
}

func BenchmarkProxyHTTP(b *testing.B) {
	benchmarkProxyHTTP(b, "benchmark payload")
}

func BenchmarkProxyHTTPBulk(b *testing.B) {
	benchmarkProxyHTTP(b, strings.Repeat("x", 64<<10))
}

func benchmarkProxyHTTP(b *testing.B, body string) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	proxyServer := benchmarkProxy(b, upstream.URL)
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		b.Fatal(err)
	}
	proxyURL.User = url.UserPassword("load-user", "load-password")
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 128,
		MaxConnsPerHost:     128,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	b.SetBytes(int64(len(body)))
	b.SetParallelism(8)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			response, err := client.Get("http://example.test/load")
			if err != nil {
				b.Error(err)
				continue
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
				b.Errorf("HTTP response: status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
			}
		}
	})
}

func BenchmarkProxyCONNECT(b *testing.B) {
	benchmarkCONNECT(b, benchmarkHTTPConnectUpstream(b))
}

func BenchmarkProxySOCKS5CONNECT(b *testing.B) {
	benchmarkCONNECT(b, benchmarkSOCKS5Upstream(b))
}

func benchmarkHTTPConnectUpstream(b *testing.B) string {
	b.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		_, _ = io.Copy(connection, connection)
	}))
	b.Cleanup(upstream.Close)
	return upstream.URL
}

func benchmarkSOCKS5Upstream(b *testing.B) string {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	var connections sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer func() { _ = connection.Close() }()
				reader := bufio.NewReader(connection)
				if _, err := fakeSOCKS5Handshake(connection, reader); err == nil {
					_, _ = io.Copy(connection, reader)
				}
			}()
		}
	}()
	b.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		connections.Wait()
	})
	return "socks5://" + listener.Addr().String()
}

func benchmarkCONNECT(b *testing.B, upstreamURL string) {
	b.Helper()
	proxyServer := benchmarkProxy(b, upstreamURL)
	address := strings.TrimPrefix(proxyServer.URL, "http://")
	auth := base64.StdEncoding.EncodeToString([]byte("load-user:load-password"))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	b.SetParallelism(8)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			connection, err := dialer.Dial("tcp", address)
			if err != nil {
				b.Error(err)
				continue
			}
			if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				b.Error(err)
				_ = connection.Close()
				continue
			}
			reader := bufio.NewReader(connection)
			_, err = io.WriteString(connection, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n")
			if err == nil {
				var status string
				status, err = reader.ReadString('\n')
				if err == nil && !strings.HasPrefix(status, "HTTP/1.1 200 ") {
					err = &unexpectedStatus{status}
				}
			}
			for err == nil {
				var line string
				line, err = reader.ReadString('\n')
				if line == "\r\n" {
					break
				}
			}
			if err == nil {
				_, err = io.WriteString(connection, "PING")
			}
			if err == nil {
				var echo [4]byte
				_, err = io.ReadFull(reader, echo[:])
				if err == nil && string(echo[:]) != "PING" {
					err = &unexpectedStatus{string(echo[:])}
				}
			}
			if closeErr := connection.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				b.Error(err)
			}
		}
	})
}

func BenchmarkProxyHTTP2CONNECT(b *testing.B) {
	upstreamURL := benchmarkHTTPConnectUpstream(b)
	proxyServer := httptest.NewUnstartedServer(benchmarkProxyHandler(b, upstreamURL))
	proxyServer.EnableHTTP2 = true
	proxyServer.StartTLS()
	b.Cleanup(proxyServer.Close)
	roots := x509.NewCertPool()
	roots.AddCert(proxyServer.Certificate())
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("load-user:load-password"))
	b.SetParallelism(8)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			request, err := http.NewRequest(http.MethodConnect, proxyServer.URL, strings.NewReader("PING"))
			if err != nil {
				b.Error(err)
				continue
			}
			request.Host = "example.test:443"
			request.Header.Set("Proxy-Authorization", auth)
			response, err := client.Do(request)
			if err != nil {
				b.Error(err)
				continue
			}
			data, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil || response.ProtoMajor != 2 || response.StatusCode != http.StatusOK || string(data) != "PING" {
				b.Errorf("HTTP/2 CONNECT: proto=%s status=%d body=%q read=%v close=%v", response.Proto, response.StatusCode, data, readErr, closeErr)
			}
		}
	})
}

type unexpectedStatus struct{ got string }

func (e *unexpectedStatus) Error() string { return "unexpected proxy response: " + e.got }

func TestProxySlowClientLoad(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
			_, _ = io.Copy(io.Discard, connection)
		}
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Proxy.MaxTunnels = 32
	cfg.Proxy.MaxTunnelsPerIP = 32
	cfg.Proxy.TunnelIdleTimeout = config.Duration(2 * time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL}}
	handler, err := NewProxyServer(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	proxyServer := httptest.NewServer(handler)
	t.Cleanup(proxyServer.Close)
	address := strings.TrimPrefix(proxyServer.URL, "http://")
	connect := func() (net.Conn, int) {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			_ = connection.Close()
			t.Fatal(err)
		}
		if _, err := io.WriteString(connection, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n"); err != nil {
			_ = connection.Close()
			t.Fatal(err)
		}
		reader := bufio.NewReader(connection)
		line, err := reader.ReadString('\n')
		if err != nil {
			_ = connection.Close()
			t.Fatal(err)
		}
		var status int
		if _, err := fmt.Sscanf(line, "HTTP/1.1 %d", &status); err != nil {
			_ = connection.Close()
			t.Fatal(err)
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				_ = connection.Close()
				t.Fatal(err)
			}
			if line == "\r\n" {
				break
			}
		}
		return connection, status
	}
	for range 32 {
		connection, status := connect()
		t.Cleanup(func() { _ = connection.Close() })
		if status != http.StatusOK {
			t.Fatalf("slow tunnel status = %d", status)
		}
	}
	connection, status := connect()
	_ = connection.Close()
	if status != http.StatusTooManyRequests {
		t.Fatalf("tunnel beyond limit status = %d, want 429", status)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		handler.tunnelLimiter.mu.Lock()
		active := handler.tunnelLimiter.total
		handler.tunnelLimiter.mu.Unlock()
		if active == 0 {
			connection, status := connect()
			_ = connection.Close()
			if status != http.StatusOK {
				t.Fatalf("tunnel after idle timeout status = %d", status)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("idle tunnels did not release all slots")
}
