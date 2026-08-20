package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dengzii/weaveflow/runtime"
)

type Handler interface {
	HandleTask(context.Context, runtime.Task) (runtime.TaskResult, error)
}

type HandlerFunc func(context.Context, runtime.Task) (runtime.TaskResult, error)

func (handler HandlerFunc) HandleTask(ctx context.Context, task runtime.Task) (runtime.TaskResult, error) {
	return handler(ctx, task)
}

type RetryClassifier interface {
	RetryTask(error) bool
}

type Config struct {
	Identity          runtime.WorkerIdentity
	Kinds             []string
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	RetryDelay        time.Duration
	Now               func() time.Time
}

type Worker struct {
	queue   runtime.TaskQueue
	handler Handler
	config  Config

	mu      sync.Mutex
	running bool
}

func New(queue runtime.TaskQueue, handler Handler, config Config) (*Worker, error) {
	if queue == nil {
		return nil, errors.New("task queue is required")
	}
	if handler == nil {
		return nil, errors.New("task handler is required")
	}
	if config.Identity.ID == "" {
		return nil, errors.New("worker identity is required")
	}
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 30 * time.Second
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = config.LeaseTTL / 3
	}
	if config.HeartbeatInterval >= config.LeaseTTL {
		return nil, errors.New("worker heartbeat interval must be less than lease TTL")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.Kinds = append([]string(nil), config.Kinds...)
	return &Worker{queue: queue, handler: handler, config: config}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	worker.mu.Lock()
	if worker.running {
		worker.mu.Unlock()
		return errors.New("worker is already running")
	}
	worker.running = true
	worker.mu.Unlock()
	defer func() {
		worker.mu.Lock()
		worker.running = false
		worker.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		claimed, _, err := worker.queue.Claim(ctx, worker.config.Identity, runtime.TaskClaimOptions{
			Kinds: worker.config.Kinds, Now: worker.now(), TTL: worker.config.LeaseTTL,
		})
		if errors.Is(err, runtime.ErrTaskNotFound) {
			if err := wait(ctx, worker.config.PollInterval); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("claim task: %w", err)
		}
		worker.execute(ctx, claimed)
	}
}

func (worker *Worker) RunOne(ctx context.Context) (bool, error) {
	claimed, _, err := worker.queue.Claim(ctx, worker.config.Identity, runtime.TaskClaimOptions{
		Kinds: worker.config.Kinds, Now: worker.now(), TTL: worker.config.LeaseTTL,
	})
	if errors.Is(err, runtime.ErrTaskNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, worker.execute(ctx, claimed)
}

func (worker *Worker) execute(ctx context.Context, task runtime.Task) error {
	if task.Lease == nil {
		return runtime.ErrTaskLeaseLost
	}
	executionCtx, cancel := context.WithCancelCause(ctx)
	heartbeatDone := make(chan error, 1)
	go worker.heartbeat(executionCtx, cancel, *task.Lease, heartbeatDone)
	result, handleErr := worker.handler.HandleTask(executionCtx, task)
	cancel(context.Canceled)
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		return heartbeatErr
	}
	if handleErr == nil {
		_, err := worker.queue.Complete(context.WithoutCancel(ctx), *task.Lease, result)
		return err
	}
	retryable := false
	if classifier, ok := worker.handler.(RetryClassifier); ok {
		retryable = classifier.RetryTask(handleErr)
	}
	_, failErr := worker.queue.Fail(context.WithoutCancel(ctx), *task.Lease, runtime.TaskFailure{
		Message: handleErr.Error(), Retryable: retryable, RetryAt: worker.now().Add(worker.config.RetryDelay),
	})
	return errors.Join(handleErr, failErr)
}

func (worker *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, lease runtime.TaskLease, done chan<- error) {
	ticker := time.NewTicker(worker.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			updated, err := worker.queue.Heartbeat(context.WithoutCancel(ctx), lease, worker.now(), worker.config.LeaseTTL)
			if err != nil {
				cancel(err)
				done <- err
				return
			}
			lease = updated
		}
	}
}

func (worker *Worker) now() time.Time {
	return worker.config.Now().UTC()
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
