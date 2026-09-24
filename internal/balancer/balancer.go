// Package balancer chooses available upstream proxies and tracks transport failures.
package balancer

import (
	"math/rand"
	"sync"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

// Selection identifies one configured upstream.
type Selection struct {
	Index    int
	Upstream config.UpstreamConfig
}

// Balancer distributes requests while temporarily excluding failed upstreams.
type Balancer struct {
	upstreams      []config.UpstreamConfig
	unhealthyUntil []time.Time
	algorithm      string
	cooldown       time.Duration
	current        int
	random         *rand.Rand
	now            func() time.Time
	mu             sync.Mutex
}

// New creates an upstream selector from validated configuration.
func New(cfg config.ProxyConfig, upstreams []config.UpstreamConfig) *Balancer {
	return &Balancer{
		upstreams:      append([]config.UpstreamConfig(nil), upstreams...),
		unhealthyUntil: make([]time.Time, len(upstreams)),
		algorithm:      cfg.Algorithm,
		cooldown:       time.Duration(cfg.FailoverCooldown),
		random:         rand.New(rand.NewSource(time.Now().UnixNano())),
		now:            time.Now,
	}
}

// Next selects a healthy upstream, excluding one already attempted for this request.
func (b *Balancer) Next(exclude int) (Selection, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	available := make([]int, 0, len(b.upstreams))
	now := b.now()
	for i := range b.upstreams {
		if i != exclude && !now.Before(b.unhealthyUntil[i]) {
			available = append(available, i)
		}
	}
	if len(available) == 0 {
		return Selection{}, false
	}
	index := available[0]
	if b.algorithm == "random" {
		index = available[b.random.Intn(len(available))]
	} else {
		for offset := range b.upstreams {
			candidate := (b.current + offset) % len(b.upstreams)
			if candidate != exclude && !now.Before(b.unhealthyUntil[candidate]) {
				index = candidate
				break
			}
		}
		b.current = (index + 1) % len(b.upstreams)
	}
	return Selection{Index: index, Upstream: b.upstreams[index]}, true
}

// MarkFailure excludes an upstream until its cooldown expires.
func (b *Balancer) MarkFailure(index int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if index >= 0 && index < len(b.upstreams) {
		b.unhealthyUntil[index] = b.now().Add(b.cooldown)
	}
}

// MarkSuccess clears a previous failure for an upstream.
func (b *Balancer) MarkSuccess(index int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if index >= 0 && index < len(b.upstreams) {
		b.unhealthyUntil[index] = time.Time{}
	}
}
