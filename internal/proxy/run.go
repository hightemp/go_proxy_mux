package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

// Run serves requests until ctx is cancelled and then closes active tunnels.
func Run(ctx context.Context, cfg *config.Config) error {
	handler, err := NewProxyServer(cfg)
	if err != nil {
		return err
	}
	defer handler.closeIdleConnections()
	address := net.JoinHostPort(cfg.Server.Host, fmt.Sprint(cfg.Server.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	listener = newLimitedListener(listener, cfg.Server.MaxConnections)
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadHeaderTimeout),
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeout),
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}},
	}
	result := make(chan error, 1)
	go func() {
		if cfg.Server.TLS.CertFile != "" {
			result <- server.ServeTLS(listener, cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
		} else {
			result <- server.Serve(listener)
		}
	}()
	select {
	case serveErr := <-result:
		handler.tunnels.closeAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Server.ShutdownTimeout))
		defer cancel()
		_ = handler.tunnels.waitFor(shutdownCtx)
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve proxy: %w", serveErr)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Server.ShutdownTimeout))
	defer cancel()
	handler.tunnels.closeAll()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = server.Close()
	}
	tunnelErr := handler.tunnels.waitFor(shutdownCtx)
	serveErr := <-result
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(shutdownErr, tunnelErr, serveErr)
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func newLimitedListener(listener net.Listener, limit int) *limitedListener {
	return &limitedListener{Listener: listener, slots: make(chan struct{}, limit)}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConnection{Conn: connection, release: func() { <-l.slots }}, nil
		default:
			_ = connection.Close()
		}
	}
}

type limitedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
