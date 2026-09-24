package socks

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSOCKS4RejectsIPv6WithoutDialing(t *testing.T) {
	_, err := DialContext(context.Background(), "socks4", "127.0.0.1:1", "[::1]:443", Credentials{}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "IPv6") {
		t.Fatalf("error = %v, want IPv6 rejection", err)
	}
}

func TestSOCKS5AuthenticationRejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer connection.Close()
		var greeting [3]byte
		if _, err := io.ReadFull(connection, greeting[:]); err != nil {
			result <- err
			return
		}
		if greeting != [3]byte{5, 1, 2} {
			result <- errors.New("wrong authentication method offered")
			return
		}
		if _, err := connection.Write([]byte{5, 2}); err != nil {
			result <- err
			return
		}
		var header [2]byte
		if _, err := io.ReadFull(connection, header[:]); err != nil {
			result <- err
			return
		}
		username := make([]byte, int(header[1]))
		if _, err := io.ReadFull(connection, username); err != nil {
			result <- err
			return
		}
		var length [1]byte
		if _, err := io.ReadFull(connection, length[:]); err != nil {
			result <- err
			return
		}
		password := make([]byte, int(length[0]))
		if _, err := io.ReadFull(connection, password); err != nil {
			result <- err
			return
		}
		if _, err := connection.Write([]byte{1, 1}); err != nil {
			result <- err
			return
		}
		result <- nil
	}()
	_, err = DialContext(context.Background(), "socks5", listener.Addr().String(), "example.test:443", Credentials{Enabled: true, Username: "test-user", Password: "test-password"}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "authentication rejected") {
		t.Fatalf("error = %v, want auth rejection", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5SetupCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			close(done)
			return
		}
		defer connection.Close()
		_, _ = io.Copy(io.Discard, connection)
		close(done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = DialContext(ctx, "socks5", listener.Addr().String(), "example.test:443", Credentials{}, time.Second)
	var networkErr net.Error
	if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &networkErr) && networkErr.Timeout()) {
		t.Fatalf("error = %v, want timeout", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SOCKS connection stayed open after cancellation")
	}
}

func TestSOCKS5IPv6Destination(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			result <- err
			return
		}
		defer connection.Close()
		var greeting [3]byte
		if _, err := io.ReadFull(connection, greeting[:]); err != nil {
			result <- err
			return
		}
		if _, err := connection.Write([]byte{5, 0}); err != nil {
			result <- err
			return
		}
		var request [22]byte
		if _, err := io.ReadFull(connection, request[:]); err != nil {
			result <- err
			return
		}
		if request[0] != 5 || request[1] != 1 || request[3] != 4 || net.IP(request[4:20]).String() != "::1" || request[20] != 1 || request[21] != 187 {
			result <- errors.New("wrong IPv6 CONNECT request")
			return
		}
		if _, err := connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
			result <- err
			return
		}
		result <- nil
	}()
	connection, err := DialContext(context.Background(), "socks5", listener.Addr().String(), "[::1]:443", Credentials{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
