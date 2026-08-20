package core

import "testing"

func TestNewConcurrencyLimiterCapsRequestedCapacity(t *testing.T) {
	limiter := NewConcurrencyLimiter(maxConcurrencyLimiterSize + 1)
	if limiter == nil {
		t.Fatal("NewConcurrencyLimiter() returned nil")
	}
	if limiter.Limit() != maxConcurrencyLimiterSize {
		t.Fatalf("limiter limit = %d, want %d", limiter.Limit(), maxConcurrencyLimiterSize)
	}
}
