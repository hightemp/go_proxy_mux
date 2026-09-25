package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func writeTestCertificate(t *testing.T) (string, string, *x509.Certificate) {
	t.Helper()
	temporary := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := temporary.TLS.Certificates[0]
	parsed := temporary.Certificate()
	temporary.Close()
	privateKey, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certPath := filepath.Join(directory, "fullchain.pem")
	keyPath := filepath.Join(directory, "privkey.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: parsed.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, parsed
}

func TestRunTLSHTTP2AndShutdown(t *testing.T) {
	upstreamURL := startRawUpstream(t, func(conn net.Conn) {
		_ = readRequestHeaders(t, bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
	})
	certPath, keyPath, certificate := writeTestCertificate(t)
	cfg := config.Default()
	cfg.Server.Port = portOfUnusedAddress(t)
	cfg.Server.TLS = config.TLSConfig{CertFile: certPath, KeyFile: keyPath}
	cfg.Server.ShutdownTimeout = config.Duration(2 * time.Second)
	cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Run(ctx, &cfg) }()
	address := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	waitForListener(t, address, result)

	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true}}
	requestBody, requestWriter := io.Pipe()
	request, err := http.NewRequest(http.MethodConnect, "https://"+address, requestBody)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "example.test:443"
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 2 || response.StatusCode != http.StatusOK {
		t.Fatalf("response=%s %d", response.Proto, response.StatusCode)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not close an active HTTP/2 CONNECT tunnel")
	}
	_ = requestWriter.Close()
	_ = response.Body.Close()
}

func portOfUnusedAddress(t *testing.T) int {
	t.Helper()
	address := unusedAddress(t)
	_, portString, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func waitForListener(t *testing.T, address string, result <-chan error) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-result:
			t.Fatalf("Run stopped before listening: %v", err)
		default:
		}
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
	}
	t.Fatal("listener did not start")
}

func TestConnectionLimitPerIP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(listener, 2, 1)
	defer func() { _ = limited.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := limited.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	serverFirst := <-accepted
	defer func() { _ = serverFirst.Close() }()
	go func() { _, _ = limited.Accept() }()
	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	one := make([]byte, 1)
	_, err = second.Read(one)
	if err != io.EOF {
		t.Fatalf("second connection read error = %v, want EOF", err)
	}
}

func TestHTTPServerNetworkSettings(t *testing.T) {
	cfg := config.Default()
	cfg.Server.MaxHeaderBytes = 8192
	cfg.Server.HTTP2MaxConcurrentStreams = 32
	cfg.Server.HTTP2SendPingTimeout = config.Duration(3 * time.Second)
	cfg.Server.HTTP2PingTimeout = config.Duration(4 * time.Second)
	cfg.Server.HTTP2WriteByteTimeout = config.Duration(5 * time.Second)
	server := newHTTPServer(&cfg, http.NotFoundHandler())
	if server.MaxHeaderBytes != 8192 || server.HTTP2.MaxConcurrentStreams != 32 || server.HTTP2.SendPingTimeout != 3*time.Second || server.HTTP2.PingTimeout != 4*time.Second || server.HTTP2.WriteByteTimeout != 5*time.Second {
		t.Fatal("HTTP server network settings were not applied")
	}
	cfg.Server.HTTP2MaxConcurrentStreams = 0
	if got := effectiveHTTP2MaxConcurrentStreams(&cfg); got != cfg.Proxy.MaxTunnelsPerIP {
		t.Fatalf("derived HTTP/2 stream limit = %d", got)
	}
}

func TestRequestHeaderLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Port = portOfUnusedAddress(t)
	cfg.Server.MaxHeaderBytes = 1024
	cfg.Upstreams = []config.UpstreamConfig{{URL: "http://127.0.0.1:1"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Run(ctx, &cfg) }()
	address := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	waitForListener(t, address, result)
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = io.WriteString(connection, "GET http://example.test/ HTTP/1.1\r\nHost: example.test\r\nX-Large: "+strings.Repeat("a", 16<<10)+"\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	status, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || !strings.Contains(status, "431") {
		t.Fatalf("status = %q, error = %v, want HTTP 431", status, err)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}
