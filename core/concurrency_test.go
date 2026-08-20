package core

import (
	"context"
	"testing"
	"time"
)

func TestNewConcurrencyLimiterCapsRequestedCapacity(t *testing.T) {
	limiter := NewConcurrencyLimiter(maxConcurrencyLimiterSize + 1)
	if limiter == nil {
		t.Fatal("NewConcurrencyLimiter() returned nil")
	}
	if limiter.Limit() != maxConcurrencyLimiterSize {
		t.Fatalf("limiter limit = %d, want %d", limiter.Limit(), maxConcurrencyLimiterSize)
	}
}

func TestConcurrencyLimiterQueuesAndReleasesWaiters(t *testing.T) {
	limiter := NewConcurrencyLimiter(1)
	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("initial Acquire() error = %v", err)
	}
	defer release()

	acquired := make(chan func(), 1)
	go func() {
		permit, acquireErr := limiter.Acquire(context.Background())
		if acquireErr == nil {
			acquired <- permit
		}
	}()

	select {
	case <-acquired:
		t.Fatal("waiter acquired before the active permit was released")
	case <-time.After(20 * time.Millisecond):
	}

	release()
	select {
	case waiterRelease := <-acquired:
		waiterRelease()
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after release")
	}
}

func TestConcurrencyLimiterCancellationDoesNotConsumeCapacity(t *testing.T) {
	limiter := NewConcurrencyLimiter(1)
	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("initial Acquire() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	acquireDone := make(chan error, 1)
	go func() {
		_, acquireErr := limiter.Acquire(ctx)
		acquireDone <- acquireErr
	}()
	cancel()
	select {
	case acquireErr := <-acquireDone:
		if acquireErr == nil {
			t.Fatal("canceled Acquire() returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Acquire() did not return")
	}
	release()

	if nextRelease, ok := limiter.TryAcquire(); !ok {
		t.Fatal("capacity remained consumed after cancellation")
	} else {
		nextRelease()
	}
}
