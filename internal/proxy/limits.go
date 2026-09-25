package proxy

import (
	"net"
	"sync"
)

type concurrentLimiter struct {
	mu        sync.Mutex
	maxTotal  int
	maxPerKey int
	total     int
	perKey    map[string]int
}

func newConcurrentLimiter(maxTotal, maxPerKey int) *concurrentLimiter {
	return &concurrentLimiter{
		maxTotal:  maxTotal,
		maxPerKey: maxPerKey,
		perKey:    make(map[string]int),
	}
}

func (l *concurrentLimiter) tryAcquire(key string) (func(), bool) {
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	if l.total >= l.maxTotal || l.perKey[key] >= l.maxPerKey {
		l.mu.Unlock()
		return nil, false
	}
	l.total++
	l.perKey[key]++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.total--
			l.perKey[key]--
			if l.perKey[key] == 0 {
				delete(l.perKey, key)
			}
			l.mu.Unlock()
		})
	}, true
}

type limitedListener struct {
	net.Listener
	limiter *concurrentLimiter
}

func newLimitedListener(listener net.Listener, maxTotal, maxPerIP int) *limitedListener {
	return &limitedListener{Listener: listener, limiter: newConcurrentLimiter(maxTotal, maxPerIP)}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		release, ok := l.limiter.tryAcquire(addressHost(connection.RemoteAddr().String()))
		if !ok {
			_ = connection.Close()
			continue
		}
		return &limitedConnection{Conn: connection, release: release}, nil
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

func (c *limitedConnection) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return nil
}

func addressHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}
