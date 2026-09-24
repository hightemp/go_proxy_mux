package proxy

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

type socksRequest struct {
	destination string
	username    string
	password    string
	method      byte
}

func fakeSOCKS(t *testing.T, version byte, handle func(net.Conn, *bufio.Reader, socksRequest) error) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(connection)
		var request socksRequest
		if version == 4 {
			request, err = fakeSOCKS4Handshake(connection, reader)
		} else {
			request, err = fakeSOCKS5Handshake(connection, reader)
		}
		if err == nil {
			err = handle(connection, reader, request)
		}
		done <- err
	}()
	return fmt.Sprintf("socks%d://%s", version, listener.Addr()), done
}

func fakeSOCKS4Handshake(connection net.Conn, reader *bufio.Reader) (socksRequest, error) {
	var header [8]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return socksRequest{}, err
	}
	if header[0] != 4 || header[1] != 1 {
		return socksRequest{}, fmt.Errorf("invalid SOCKS4 command")
	}
	user, err := reader.ReadBytes(0)
	if err != nil {
		return socksRequest{}, err
	}
	host := net.IP(header[4:8]).String()
	if header[4] == 0 && header[5] == 0 && header[6] == 0 && header[7] != 0 {
		domain, err := reader.ReadBytes(0)
		if err != nil {
			return socksRequest{}, err
		}
		host = string(domain[:len(domain)-1])
	}
	if _, err := connection.Write([]byte{0, 0x5a, 0, 0, 0, 0, 0, 0}); err != nil {
		return socksRequest{}, err
	}
	return socksRequest{destination: net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(header[2:4]))), username: string(user[:len(user)-1])}, nil
}

func fakeSOCKS5Handshake(connection net.Conn, reader *bufio.Reader) (socksRequest, error) {
	var greeting [3]byte
	if _, err := io.ReadFull(reader, greeting[:]); err != nil {
		return socksRequest{}, err
	}
	if greeting[0] != 5 || greeting[1] != 1 || (greeting[2] != 0 && greeting[2] != 2) {
		return socksRequest{}, fmt.Errorf("invalid SOCKS5 greeting")
	}
	if _, err := connection.Write([]byte{5, greeting[2]}); err != nil {
		return socksRequest{}, err
	}
	result := socksRequest{method: greeting[2]}
	if greeting[2] == 2 {
		var authHeader [2]byte
		if _, err := io.ReadFull(reader, authHeader[:]); err != nil {
			return socksRequest{}, err
		}
		if authHeader[0] != 1 {
			return socksRequest{}, fmt.Errorf("invalid SOCKS5 auth version")
		}
		username := make([]byte, int(authHeader[1]))
		if _, err := io.ReadFull(reader, username); err != nil {
			return socksRequest{}, err
		}
		var passwordLength [1]byte
		if _, err := io.ReadFull(reader, passwordLength[:]); err != nil {
			return socksRequest{}, err
		}
		password := make([]byte, int(passwordLength[0]))
		if _, err := io.ReadFull(reader, password); err != nil {
			return socksRequest{}, err
		}
		result.username, result.password = string(username), string(password)
		if _, err := connection.Write([]byte{1, 0}); err != nil {
			return socksRequest{}, err
		}
	}
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return socksRequest{}, err
	}
	if header[0] != 5 || header[1] != 1 || header[2] != 0 {
		return socksRequest{}, fmt.Errorf("invalid SOCKS5 command")
	}
	var host string
	switch header[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(reader, address); err != nil {
			return socksRequest{}, err
		}
		host = net.IP(address).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return socksRequest{}, err
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, address); err != nil {
			return socksRequest{}, err
		}
		host = string(address)
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(reader, address); err != nil {
			return socksRequest{}, err
		}
		host = net.IP(address).String()
	default:
		return socksRequest{}, fmt.Errorf("invalid SOCKS5 address type")
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return socksRequest{}, err
	}
	result.destination = net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port[:])))
	if _, err := connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return socksRequest{}, err
	}
	return result, nil
}

func TestSOCKSUpstreamsHTTPAndConnect(t *testing.T) {
	tests := []struct {
		name    string
		version byte
		auth    config.UpstreamAuth
	}{
		{name: "socks4", version: 4},
		{name: "socks4 USERID", version: 4, auth: config.UpstreamAuth{Enabled: true, Username: "user-id"}},
		{name: "socks5", version: 5},
		{name: "socks5 username password", version: 5, auth: config.UpstreamAuth{Enabled: true, Username: "user", Password: "password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := func(request socksRequest, destination string) error {
				if request.destination != destination {
					return fmt.Errorf("destination = %q, want %q", request.destination, destination)
				}
				if request.username != tt.auth.Username || request.password != tt.auth.Password {
					return fmt.Errorf("unexpected SOCKS authentication values")
				}
				if tt.version == 5 && (request.method == 2) != tt.auth.Enabled {
					return fmt.Errorf("wrong SOCKS5 auth method")
				}
				return nil
			}
			t.Run("HTTP", func(t *testing.T) {
				upstreamURL, done := fakeSOCKS(t, tt.version, func(connection net.Conn, reader *bufio.Reader, request socksRequest) error {
					if err := check(request, "example.test:80"); err != nil {
						return err
					}
					line, err := reader.ReadString('\n')
					if err != nil {
						return err
					}
					if line != "GET /path HTTP/1.1\r\n" {
						return fmt.Errorf("origin request = %q", line)
					}
					headers := readRequestHeaders(t, reader)
					if !strings.Contains(headers, "Host: example.test\r\n") || strings.Contains(strings.ToLower(headers), "proxy-authorization:") {
						return fmt.Errorf("incorrect origin request headers")
					}
					_, err = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK")
					return err
				})
				cfg := config.Default()
				cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL, Auth: tt.auth}}
				proxyServer := newTestProxy(t, &cfg)
				response, err := clientThroughProxy(t, proxyServer.URL, "", "").Get("http://example.test/path")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusOK || string(body) != "OK" {
					t.Fatalf("response=%d %q", response.StatusCode, body)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
			t.Run("CONNECT", func(t *testing.T) {
				upstreamURL, done := fakeSOCKS(t, tt.version, func(connection net.Conn, reader *bufio.Reader, request socksRequest) error {
					if err := check(request, "example.test:443"); err != nil {
						return err
					}
					if _, err := io.WriteString(connection, "READY"); err != nil {
						return err
					}
					payload := make([]byte, 4)
					if _, err := io.ReadFull(reader, payload); err != nil {
						return err
					}
					if string(payload) != "PING" {
						return fmt.Errorf("tunnel payload = %q", payload)
					}
					_, err := io.WriteString(connection, "PONG")
					return err
				})
				cfg := config.Default()
				cfg.Upstreams = []config.UpstreamConfig{{URL: upstreamURL, Auth: tt.auth}}
				proxyServer := newTestProxy(t, &cfg)
				connection, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyServer.URL, "http://"), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := io.WriteString(connection, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\nPING"); err != nil {
					t.Fatal(err)
				}
				reader := bufio.NewReader(connection)
				if status := readStatusHeaders(t, reader); !strings.HasPrefix(status, "HTTP/1.1 200 ") {
					t.Fatalf("status=%q", status)
				}
				payload := make([]byte, len("READYPONG"))
				if _, err := io.ReadFull(reader, payload); err != nil {
					t.Fatal(err)
				}
				if string(payload) != "READYPONG" {
					t.Fatalf("tunnel payload = %q", payload)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestSOCKS4IPv6FallsBackWithoutCoolingDown(t *testing.T) {
	upstreamURL, done := fakeSOCKS(t, 5, func(connection net.Conn, reader *bufio.Reader, request socksRequest) error {
		if request.destination != "[::1]:80" {
			return fmt.Errorf("destination = %q", request.destination)
		}
		line, err := reader.ReadString('\n')
		if err != nil || line != "GET /path HTTP/1.1\r\n" {
			return fmt.Errorf("request line = %q, error = %v", line, err)
		}
		_ = readRequestHeaders(t, reader)
		_, err = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK")
		return err
	})
	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{URL: "socks4://127.0.0.1:1"}, {URL: upstreamURL}}
	handler, err := NewProxyServer(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	inbound := httptest.NewServer(handler)
	defer inbound.Close()
	response, err := clientThroughProxy(t, inbound.URL, "", "").Get("http://[::1]/path")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	first, ok := handler.balancer.Next(1)
	if !ok || first.Index != 0 {
		t.Fatal("SOCKS4 was incorrectly marked unhealthy for an IPv6 destination")
	}
}
