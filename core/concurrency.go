package core

import (
	"context"
	"fmt"
)

type ConcurrencyLimiter struct {
	limit int
	slots chan struct{}
}

type ConcurrencyWaitObserver func(limit int)

func NewConcurrencyLimiter(limit int) *ConcurrencyLimiter {
	if limit <= 0 {
		return nil
	}
	return &ConcurrencyLimiter{limit: limit, slots: make(chan struct{}, limit)}
}

func (limiter *ConcurrencyLimiter) Limit() int {
	if limiter == nil {
		return 0
	}
	return limiter.limit
}

func (limiter *ConcurrencyLimiter) Acquire(ctx context.Context) (func(), error) {
	if limiter == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case limiter.slots <- struct{}{}:
		return func() { <-limiter.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (limiter *ConcurrencyLimiter) TryAcquire() (func(), bool) {
	if limiter == nil {
		return func() {}, true
	}
	select {
	case limiter.slots <- struct{}{}:
		return func() { <-limiter.slots }, true
	default:
		return nil, false
	}
}

type toolConcurrencyConfigKey struct{}

type toolConcurrencyConfig struct {
	limiter  *ConcurrencyLimiter
	observer ConcurrencyWaitObserver
}

type toolExecutionPermitKey struct{}

type toolExecutionPermit struct {
	slot chan struct{}
}

func WithToolConcurrencyLimiter(ctx context.Context, limiter *ConcurrencyLimiter, observer ConcurrencyWaitObserver) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolConcurrencyConfigKey{}, toolConcurrencyConfig{limiter: limiter, observer: observer})
}

func ToolConcurrencyLimiterFromContext(ctx context.Context) *ConcurrencyLimiter {
	if ctx == nil {
		return nil
	}
	config, _ := ctx.Value(toolConcurrencyConfigKey{}).(toolConcurrencyConfig)
	return config.limiter
}

func ToolExecutionConcurrencyLimit(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if _, ok := ctx.Value(toolExecutionPermitKey{}).(*toolExecutionPermit); ok {
		return 1
	}
	if limiter := ToolConcurrencyLimiterFromContext(ctx); limiter != nil {
		return limiter.Limit()
	}
	return 0
}

func AcquireToolExecution(ctx context.Context, mode ToolExecutionMode) (context.Context, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, _ := ctx.Value(toolConcurrencyConfigKey{}).(toolConcurrencyConfig)
	if permit, ok := ctx.Value(toolExecutionPermitKey{}).(*toolExecutionPermit); ok {
		if mode == ToolExecutionComposite {
			return ctx, func() {}, nil
		}
		select {
		case permit.slot <- struct{}{}:
			return ctx, func() { <-permit.slot }, nil
		default:
			if config.observer != nil {
				config.observer(1)
			}
		}
		select {
		case permit.slot <- struct{}{}:
			return ctx, func() { <-permit.slot }, nil
		case <-ctx.Done():
			return ctx, nil, fmt.Errorf("acquire nested tool execution capacity: %w", ctx.Err())
		}
	}
	limiter := config.limiter
	if limiter == nil {
		return ctx, func() {}, nil
	}
	var release func()
	if release, ok := limiter.TryAcquire(); ok {
		return withToolExecutionPermit(ctx, mode), release, nil
	}
	if config.observer != nil {
		config.observer(limiter.Limit())
	}
	release, err := limiter.Acquire(ctx)
	if err != nil {
		return ctx, nil, fmt.Errorf("acquire tool execution capacity: %w", err)
	}
	return withToolExecutionPermit(ctx, mode), release, nil
}

func withToolExecutionPermit(ctx context.Context, mode ToolExecutionMode) context.Context {
	if mode != ToolExecutionComposite {
		return ctx
	}
	return context.WithValue(ctx, toolExecutionPermitKey{}, &toolExecutionPermit{slot: make(chan struct{}, 1)})
}
