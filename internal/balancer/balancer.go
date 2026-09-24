// Package balancer chooses an upstream proxy for each request.
package balancer

import (
	"math/rand"
	"sync"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

// LoadBalancer selects the next upstream proxy.
type LoadBalancer interface {
	Next() *config.UpstreamConfig
}

// RoundRobinBalancer selects upstreams in cyclic order.
type RoundRobinBalancer struct {
	upstreams []config.UpstreamConfig
	current   int
	mutex     sync.Mutex
}

// NewRoundRobinBalancer creates a round-robin selector.
func NewRoundRobinBalancer(upstreams []config.UpstreamConfig) *RoundRobinBalancer {
	return &RoundRobinBalancer{
		upstreams: upstreams,
		current:   0,
	}
}

// Next returns the next configured upstream, or nil if none exist.
func (rb *RoundRobinBalancer) Next() *config.UpstreamConfig {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if len(rb.upstreams) == 0 {
		return nil
	}

	upstream := &rb.upstreams[rb.current]
	rb.current = (rb.current + 1) % len(rb.upstreams)
	return upstream
}

// RandomBalancer selects upstreams randomly.
type RandomBalancer struct {
	upstreams []config.UpstreamConfig
	rand      *rand.Rand
	mutex     sync.Mutex
}

// NewRandomBalancer creates a random selector.
func NewRandomBalancer(upstreams []config.UpstreamConfig) *RandomBalancer {
	return &RandomBalancer{
		upstreams: upstreams,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Next returns a randomly selected upstream, or nil if none exist.
func (rb *RandomBalancer) Next() *config.UpstreamConfig {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if len(rb.upstreams) == 0 {
		return nil
	}

	index := rb.rand.Intn(len(rb.upstreams))
	return &rb.upstreams[index]
}

// CreateBalancer constructs the configured selector.
func CreateBalancer(algorithm string, upstreams []config.UpstreamConfig) LoadBalancer {
	switch algorithm {
	case "random":
		return NewRandomBalancer(upstreams)
	case "roundrobin":
		return NewRoundRobinBalancer(upstreams)
	default:
		return NewRoundRobinBalancer(upstreams)
	}
}
