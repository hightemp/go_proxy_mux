package proxy

import (
	"container/list"
	"sync"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

type authFailure struct {
	ip           string
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

type authFailureLimiter struct {
	mu          sync.Mutex
	byIP        map[string]*list.Element
	oldestFirst *list.List
	maxAttempts int
	window      time.Duration
	block       time.Duration
	maxIPs      int
	now         func() time.Time
}

func newAuthFailureLimiter(cfg config.AuthConfig) *authFailureLimiter {
	return &authFailureLimiter{
		byIP:        make(map[string]*list.Element),
		oldestFirst: list.New(),
		maxAttempts: cfg.MaxFailedAttempts,
		window:      time.Duration(cfg.FailureWindow),
		block:       time.Duration(cfg.BlockDuration),
		maxIPs:      cfg.MaxTrackedIPs,
		now:         time.Now,
	}
}

// attempt records an authentication result and returns the remaining block time.
// Even valid credentials are rejected while their source IP is blocked.
func (l *authFailureLimiter) attempt(ip string, valid bool) time.Duration {
	if ip == "" {
		ip = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	element := l.byIP[ip]
	var entry *authFailure
	if element != nil {
		l.oldestFirst.MoveToBack(element)
		entry = element.Value.(*authFailure)
	}
	if entry != nil && now.Before(entry.blockedUntil) {
		return entry.blockedUntil.Sub(now)
	}
	if valid {
		if element != nil {
			l.oldestFirst.Remove(element)
			delete(l.byIP, ip)
		}
		return 0
	}
	if entry == nil {
		if len(l.byIP) >= l.maxIPs {
			oldest := l.oldestFirst.Front()
			delete(l.byIP, oldest.Value.(*authFailure).ip)
			l.oldestFirst.Remove(oldest)
		}
		entry = &authFailure{ip: ip, windowStart: now}
		l.byIP[ip] = l.oldestFirst.PushBack(entry)
	} else if !entry.blockedUntil.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry.count = 0
		entry.windowStart = now
		entry.blockedUntil = time.Time{}
	}
	entry.count++
	if entry.count >= l.maxAttempts {
		entry.blockedUntil = now.Add(l.block)
		return l.block
	}
	return 0
}
