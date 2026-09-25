package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func startRawUpstream(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		handle(connection)
	}()
	return "http://" + listener.Addr().String()
}

func readRequestHeaders(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var result strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Errorf("read request headers: %v", err)
			return result.String()
		}
		result.WriteString(line)
		if line == "\r\n" {
			return result.String()
		}
	}
}

func readStatusHeaders(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var result strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read status headers: %v", err)
		}
		result.WriteString(line)
		if line == "\r\n" {
			return result.String()
		}
	}
}

func TestHTTP1ConnectPreservesBufferedBytes(t *testing.T) {
	received := make(chan string, 1)
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		_ = readRequestHeaders(t, reader)
		_, _ = conn.Write([]byte("HTTP/1.1 200 Con"))
		time.Sleep(10 * time.Millisecond)
		_, _ = conn.Write([]byte("nection Established\r\nX-Test: ok\r\n\r\nHELLO"))
		payload := make([]byte, 4)
		if _, err := io.ReadFull(reader, payload); err != nil {
			t.Errorf("upstream read: %v", err)
			return
		}
		received <- string(payload)
		_, _ = conn.Write([]byte("PONG"))
	})
	cfg := config.Default()
	cfg.Proxy.TunnelIdleTimeout = config.Duration(time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	proxyServer := newTestProxy(t, &cfg)
	address := strings.TrimPrefix(proxyServer.URL, "http://")
	client, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\nPING"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	headers := readStatusHeaders(t, reader)
	if !strings.HasPrefix(headers, "HTTP/1.1 200 ") {
		t.Fatalf("response status = %q", strings.Split(headers, "\r\n")[0])
	}
	payload := make([]byte, len("HELLOPONG"))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "HELLOPONG" {
		t.Fatalf("tunnel payload = %q", payload)
	}
	if got := <-received; got != "PING" {
		t.Fatalf("upstream received = %q", got)
	}
}

func TestConnectRejectsStatusContaining200InHeader(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nX-Note: 200\r\n\r\n")
	})
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	headers := readStatusHeaders(t, bufio.NewReader(client))
	if !strings.HasPrefix(headers, "HTTP/1.1 502 ") {
		t.Fatalf("status = %q", strings.Split(headers, "\r\n")[0])
	}
}

func TestConnectRetriesAlternateBeforeSuccess(t *testing.T) {
	first := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
	})
	second := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\nOK")
	})
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: first}, {URL: second}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	reader := bufio.NewReader(client)
	if status := readStatusHeaders(t, reader); !strings.HasPrefix(status, "HTTP/1.1 200 ") {
		t.Fatalf("status = %q", status)
	}
	data := make([]byte, 2)
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != "OK" {
		t.Fatalf("data = %q, error = %v", data, err)
	}
}

func TestConnectResponseHeaderLimit(t *testing.T) {
	message := "HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("a", maxConnectResponseHeaderBytes) + "\r\n\r\n"
	if _, err := readConnectStatus(bufio.NewReader(strings.NewReader(message))); err == nil {
		t.Fatal("oversized CONNECT response headers were accepted")
	}
}

func TestConnectSetupTimeout(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		time.Sleep(1500 * time.Millisecond)
	})
	cfg := config.Default()
	cfg.Proxy.Timeout = 1
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	status := readStatusHeaders(t, bufio.NewReader(client))
	if !strings.HasPrefix(status, "HTTP/1.1 504 ") {
		t.Fatalf("status = %q", strings.Split(status, "\r\n")[0])
	}
}

func TestConnectResponseHeaderTimeout(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		time.Sleep(300 * time.Millisecond)
	})
	cfg := config.Default()
	cfg.Proxy.Timeout = 5
	cfg.Proxy.ResponseHeaderTimeout = config.Duration(80 * time.Millisecond)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	start := time.Now()
	status := readStatusHeaders(t, bufio.NewReader(client))
	if !strings.HasPrefix(status, "HTTP/1.1 504 ") || time.Since(start) > time.Second {
		t.Fatalf("status = %q after %s", strings.Split(status, "\r\n")[0], time.Since(start))
	}
}

func TestConnectTunnelIdleTimeout(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
	})
	cfg := config.Default()
	cfg.Proxy.TunnelIdleTimeout = config.Duration(100 * time.Millisecond)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	reader := bufio.NewReader(client)
	if status := readStatusHeaders(t, reader); !strings.HasPrefix(status, "HTTP/1.1 200 ") {
		t.Fatalf("status = %q", status)
	}
	_, err = reader.Read(make([]byte, 1))
	if err != io.EOF {
		t.Fatalf("idle tunnel read error = %v, want EOF", err)
	}
}

func TestHTTPSUpstreamConnect(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("method = %q", r.Method)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\nDATA")
	}))
	defer upstream.Close()
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstream.URL, TLSCAFile: certPath}}
	proxyServer := newTestProxy(t, &cfg)
	client, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(client, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
	reader := bufio.NewReader(client)
	if status := readStatusHeaders(t, reader); !strings.HasPrefix(status, "HTTP/1.1 200 ") {
		t.Fatalf("status = %q", status)
	}
	data := make([]byte, 4)
	if _, err := io.ReadFull(reader, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "DATA" {
		t.Fatalf("data = %q", data)
	}
}

func TestHTTP2Connect(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		_ = readRequestHeaders(t, reader)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		message := make([]byte, 4)
		if _, err := io.ReadFull(reader, message); err != nil {
			t.Errorf("read tunnel request: %v", err)
			return
		}
		_, _ = conn.Write(message)
	})
	cfg := config.Default()
	cfg.Auth = config.AuthConfig{Enabled: true, Username: "h2-user", Password: "strong-h2-password"}
	cfg.Proxy.TunnelIdleTimeout = config.Duration(time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	handler, err := NewProxyServer(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	tlsServer := httptest.NewUnstartedServer(handler)
	tlsServer.EnableHTTP2 = true
	tlsServer.StartTLS()
	defer tlsServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true}}
	unauthorized, err := http.NewRequest(http.MethodConnect, tlsServer.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Host = "example.test:443"
	unauthorizedResponse, err := client.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	if unauthorizedResponse.ProtoMajor != 2 || unauthorizedResponse.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("unauthorized response = %s %d", unauthorizedResponse.Proto, unauthorizedResponse.StatusCode)
	}
	_ = unauthorizedResponse.Body.Close()
	bodyReader, bodyWriter := io.Pipe()
	request, err := http.NewRequest(http.MethodConnect, tlsServer.URL, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "example.test:443"
	request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("h2-user:strong-h2-password")))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.ProtoMajor != 2 || response.StatusCode != http.StatusOK {
		t.Fatalf("response = %s %d", response.Proto, response.StatusCode)
	}
	if _, err := bodyWriter.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	if err := bodyWriter.Close(); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4)
	if _, err := io.ReadFull(response.Body, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != "PING" {
		t.Fatalf("response data = %q", data)
	}
}
