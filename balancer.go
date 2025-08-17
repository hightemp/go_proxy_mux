package main

import (
	"math/rand"
	"sync"
	"time"
)

type LoadBalancer interface {
	Next() *UpstreamConfig
}

type RoundRobinBalancer struct {
	upstreams []UpstreamConfig
	current   int
	mutex     sync.Mutex
}

func NewRoundRobinBalancer(upstreams []UpstreamConfig) *RoundRobinBalancer {
	return &RoundRobinBalancer{
		upstreams: upstreams,
		current:   0,
	}
}

func (rb *RoundRobinBalancer) Next() *UpstreamConfig {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if len(rb.upstreams) == 0 {
		return nil
	}

	upstream := &rb.upstreams[rb.current]
	rb.current = (rb.current + 1) % len(rb.upstreams)
	return upstream
}

type RandomBalancer struct {
	upstreams []UpstreamConfig
	rand      *rand.Rand
	mutex     sync.Mutex
}

func NewRandomBalancer(upstreams []UpstreamConfig) *RandomBalancer {
	return &RandomBalancer{
		upstreams: upstreams,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (rb *RandomBalancer) Next() *UpstreamConfig {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if len(rb.upstreams) == 0 {
		return nil
	}

	index := rb.rand.Intn(len(rb.upstreams))
	return &rb.upstreams[index]
}

func CreateBalancer(algorithm string, upstreams []UpstreamConfig) LoadBalancer {
	switch algorithm {
	case "random":
		return NewRandomBalancer(upstreams)
	case "roundrobin":
		return NewRoundRobinBalancer(upstreams)
	default:
		return NewRoundRobinBalancer(upstreams)
	}
}
