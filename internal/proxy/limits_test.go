package proxy

import "testing"

func TestConcurrentLimiterGlobalAndPerIP(t *testing.T) {
	limiter := newConcurrentLimiter(2, 1)
	firstRelease, ok := limiter.tryAcquire("192.0.2.1")
	if !ok {
		t.Fatal("first connection rejected")
	}
	if _, ok := limiter.tryAcquire("192.0.2.1"); ok {
		t.Fatal("same IP exceeded its limit")
	}
	secondRelease, ok := limiter.tryAcquire("192.0.2.2")
	if !ok {
		t.Fatal("other IP rejected below total limit")
	}
	if _, ok := limiter.tryAcquire("192.0.2.3"); ok {
		t.Fatal("global limit was exceeded")
	}
	firstRelease()
	firstRelease() // Releasing a closed connection twice must not corrupt counts.
	thirdRelease, ok := limiter.tryAcquire("192.0.2.1")
	if !ok {
		t.Fatal("IP slot was not released")
	}
	secondRelease()
	thirdRelease()
}
