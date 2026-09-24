package proxy

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type tunnelRegistry struct {
	mu      sync.Mutex
	closing bool
	active  map[*tunnelPair]struct{}
	wait    sync.WaitGroup
}

type tunnelPair struct {
	close func()
}

func newTunnelRegistry() *tunnelRegistry {
	return &tunnelRegistry{active: make(map[*tunnelPair]struct{})}
}

func (r *tunnelRegistry) track(closePair func()) (func(), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return nil, false
	}
	pair := &tunnelPair{close: closePair}
	r.active[pair] = struct{}{}
	r.wait.Add(1)
	return func() {
		r.mu.Lock()
		if _, exists := r.active[pair]; exists {
			delete(r.active, pair)
			r.wait.Done()
		}
		r.mu.Unlock()
	}, true
}

func (r *tunnelRegistry) closeAll() {
	r.mu.Lock()
	r.closing = true
	pairs := make([]*tunnelPair, 0, len(r.active))
	for pair := range r.active {
		pairs = append(pairs, pair)
	}
	r.mu.Unlock()
	for _, pair := range pairs {
		pair.close()
	}
}

func (r *tunnelRegistry) waitFor(ctx context.Context) error {
	done := make(chan struct{})
	go func() { r.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type activityReader struct {
	io.Reader
	touch func()
}

func (r activityReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.touch()
	}
	return n, err
}

type flushingWriter struct {
	io.Writer
	flush func() error
}

func (w flushingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err != nil || n == 0 {
		return n, err
	}
	return n, w.flush()
}

type idleCloser struct {
	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
}

func newIdleCloser(duration time.Duration, closePair func()) *idleCloser {
	c := &idleCloser{}
	c.timer = time.AfterFunc(duration, closePair)
	return c
}

func (c *idleCloser) touch(duration time.Duration) {
	c.mu.Lock()
	if !c.stopped {
		c.timer.Reset(duration)
	}
	c.mu.Unlock()
}

func (c *idleCloser) stop() {
	c.mu.Lock()
	c.stopped = true
	c.timer.Stop()
	c.mu.Unlock()
}

func closeWrite(connection net.Conn) error {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return nil
}

func relay(
	destination net.Conn,
	sourceToDestination io.Reader,
	clientWriter io.Writer,
	sourceToClient io.Reader,
	halfCloseClient func() error,
	closePair func(),
	idleTimeout time.Duration,
) {
	idle := newIdleCloser(idleTimeout, closePair)
	defer idle.stop()
	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(destination, activityReader{Reader: sourceToDestination, touch: func() { idle.touch(idleTimeout) }})
		if err == nil {
			err = closeWrite(destination)
		}
		results <- err
	}()
	go func() {
		_, err := io.Copy(clientWriter, activityReader{Reader: sourceToClient, touch: func() { idle.touch(idleTimeout) }})
		if err == nil && halfCloseClient != nil {
			err = halfCloseClient()
		}
		results <- err
	}()
	first := <-results
	if first != nil {
		closePair()
	}
	second := <-results
	if first != nil || second != nil {
		log.Printf("Tunnel ended after I/O error")
	}
}

func (ps *ProxyServer) relayHTTP1(w http.ResponseWriter, r *http.Request, upstream net.Conn, upstreamReader *bufio.Reader) {
	hijacker := w.(http.Hijacker)
	client, clientBuffer, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	closePair := func() { _ = client.Close(); _ = upstream.Close() }
	release, ok := ps.tunnels.track(closePair)
	if !ok {
		closePair()
		return
	}
	defer release()
	defer closePair()
	_ = client.SetDeadline(time.Time{})
	if _, err := clientBuffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := clientBuffer.Flush(); err != nil {
		return
	}
	relay(upstream, clientBuffer.Reader, client, upstreamReader, func() error { return closeWrite(client) }, closePair, time.Duration(ps.config.Proxy.TunnelIdleTimeout))
}

func (ps *ProxyServer) relayHTTP2(w http.ResponseWriter, r *http.Request, upstream net.Conn, upstreamReader *bufio.Reader) {
	body := r.Body
	if body == nil {
		body = http.NoBody
	}
	controller := http.NewResponseController(w)
	closePair := func() {
		_ = body.Close()
		_ = upstream.Close()
	}
	release, ok := ps.tunnels.track(closePair)
	if !ok {
		closePair()
		http.Error(w, "Server shutting down", http.StatusServiceUnavailable)
		return
	}
	defer release()
	defer closePair()
	stopCancel := context.AfterFunc(r.Context(), closePair)
	defer stopCancel()
	w.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	writer := flushingWriter{Writer: w, flush: controller.Flush}
	relay(upstream, body, writer, upstreamReader, nil, closePair, time.Duration(ps.config.Proxy.TunnelIdleTimeout))
}
