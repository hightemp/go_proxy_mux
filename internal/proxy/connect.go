package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/balancer"
	"github.com/hightemp/go_proxy_mux/internal/config"
)

const maxConnectResponseHeaderBytes = 64 << 10

func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request, selected balancer.Selection) {
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	hostname, port, err := net.SplitHostPort(target)
	if err != nil || hostname == "" || strings.ContainsAny(hostname, "\r\n") {
		http.Error(w, "Invalid CONNECT destination", http.StatusBadRequest)
		return
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		http.Error(w, "Invalid CONNECT destination port", http.StatusBadRequest)
		return
	}
	if r.ProtoMajor != 1 && r.ProtoMajor != 2 {
		http.Error(w, "Unsupported HTTP version", http.StatusHTTPVersionNotSupported)
		return
	}
	if r.ProtoMajor == 1 {
		if _, ok := w.(http.Hijacker); !ok {
			http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
			return
		}
	}
	select {
	case ps.tunnelSlots <- struct{}{}:
		defer func() { <-ps.tunnelSlots }()
	default:
		http.Error(w, "Tunnel limit exceeded", http.StatusTooManyRequests)
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		connection, reader, setupErr := ps.dialConnect(r.Context(), selected.Index, target)
		if setupErr == nil {
			ps.balancer.MarkSuccess(selected.Index)
			if r.ProtoMajor == 2 {
				ps.relayHTTP2(w, r, connection, reader)
			} else {
				ps.relayHTTP1(w, r, connection, reader)
			}
			return
		}
		if r.Context().Err() != nil {
			return
		}
		ps.balancer.MarkFailure(selected.Index)
		log.Printf("Upstream %d CONNECT setup failed: %v", selected.Index, sanitizeError(setupErr))
		if attempt == 0 {
			if alternate, ok := ps.balancer.Next(selected.Index); ok {
				selected = alternate
				continue
			}
		}
		writeUpstreamError(w, setupErr)
		return
	}
}

func (ps *ProxyServer) dialConnect(ctx context.Context, index int, target string) (net.Conn, *bufio.Reader, error) {
	upstream := ps.config.Upstreams[index]
	proxyURL, err := config.UpstreamURL(upstream)
	if err != nil {
		return nil, nil, err
	}
	timeout := time.Duration(ps.config.Proxy.Timeout) * time.Second
	setupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := (&net.Dialer{Timeout: timeout}).DialContext(setupCtx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, nil, setupError(setupCtx, err)
	}
	connection := net.Conn(raw)
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	stopCancel := context.AfterFunc(setupCtx, func() { _ = connection.Close() })
	defer stopCancel()
	if err := connection.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, err
	}
	if proxyURL.Scheme == "https" {
		tlsConfig := &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
		if ps.upstreamTLS[index] != nil {
			tlsConfig.RootCAs = ps.upstreamTLS[index].RootCAs
		}
		secured := tls.Client(connection, tlsConfig)
		if err := secured.HandshakeContext(setupCtx); err != nil {
			return nil, nil, setupError(setupCtx, err)
		}
		connection = secured
	}
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if upstream.Auth.Enabled {
		encoded := base64.StdEncoding.EncodeToString([]byte(upstream.Auth.Username + ":" + upstream.Auth.Password))
		request += "Proxy-Authorization: Basic " + encoded + "\r\n"
	}
	if _, err := io.WriteString(connection, request+"\r\n"); err != nil {
		return nil, nil, setupError(setupCtx, err)
	}
	reader := bufio.NewReader(connection)
	status, err := readConnectStatus(reader)
	if err != nil {
		return nil, nil, setupError(setupCtx, err)
	}
	if status != http.StatusOK {
		return nil, nil, fmt.Errorf("upstream CONNECT status %d", status)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, nil, err
	}
	success = true
	return connection, reader, nil
}

func setupError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func readConnectStatus(reader *bufio.Reader) (int, error) {
	remaining := maxConnectResponseHeaderBytes
	statusLine, err := readLimitedLine(reader, &remaining)
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(statusLine)
	if len(parts) < 2 || (parts[0] != "HTTP/1.1" && parts[0] != "HTTP/1.0") {
		return 0, fmt.Errorf("invalid upstream CONNECT status line")
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil || status < 100 || status > 599 {
		return 0, fmt.Errorf("invalid upstream CONNECT status")
	}
	for {
		line, err := readLimitedLine(reader, &remaining)
		if err != nil {
			return 0, err
		}
		if line == "" {
			return status, nil
		}
		if !strings.Contains(line, ":") {
			return 0, fmt.Errorf("invalid upstream CONNECT header")
		}
	}
}

func readLimitedLine(reader *bufio.Reader, remaining *int) (string, error) {
	var line []byte
	for {
		part, more, err := reader.ReadLine()
		if err != nil {
			return "", err
		}
		*remaining -= len(part)
		if !more {
			*remaining -= 2
		}
		if *remaining < 0 {
			return "", fmt.Errorf("upstream CONNECT headers exceed %d bytes", maxConnectResponseHeaderBytes)
		}
		line = append(line, part...)
		if !more {
			return string(line), nil
		}
	}
}
