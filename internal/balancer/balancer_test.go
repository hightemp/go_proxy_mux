package balancer

import (
	"testing"
	"time"

	"github.com/hightemp/go_proxy_mux/internal/config"
)

func TestRoundRobinCooldown(t *testing.T) {
	upstreams := []config.UpstreamConfig{{URL: "http://one:80"}, {URL: "http://two:80"}}
	cfg := config.Default()
	b := New(cfg.Proxy, upstreams)
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	first, ok := b.Next(-1)
	if !ok || first.Index != 0 {
		t.Fatalf("first = %+v, %v", first, ok)
	}
	b.MarkFailure(0)
	second, ok := b.Next(-1)
	if !ok || second.Index != 1 {
		t.Fatalf("after failure = %+v, %v", second, ok)
	}
	if _, ok := b.Next(1); ok {
		t.Fatal("failed upstream selected during cooldown")
	}
	now = now.Add(time.Duration(cfg.Proxy.FailoverCooldown))
	recovered, ok := b.Next(1)
	if !ok || recovered.Index != 0 {
		t.Fatalf("recovered = %+v, %v", recovered, ok)
	}
}

func TestRandomSkipsFailedUpstream(t *testing.T) {
	cfg := config.Default()
	cfg.Proxy.Algorithm = "random"
	b := New(cfg.Proxy, []config.UpstreamConfig{{URL: "http://one:80"}, {URL: "http://two:80"}})
	b.MarkFailure(0)
	for range 100 {
		selected, ok := b.Next(-1)
		if !ok || selected.Index != 1 {
			t.Fatalf("selected = %+v, %v", selected, ok)
		}
	}
}
