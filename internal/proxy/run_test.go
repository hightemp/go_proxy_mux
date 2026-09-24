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

func TestConnectionLimit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(listener, 1)
	defer limited.Close()
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
	defer first.Close()
	serverFirst := <-accepted
	defer serverFirst.Close()
	go func() { _, _ = limited.Accept() }()
	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	one := make([]byte, 1)
	_, err = second.Read(one)
	if err != io.EOF {
		t.Fatalf("second connection read error = %v, want EOF", err)
	}
}
