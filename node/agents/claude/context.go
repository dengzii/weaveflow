package claude

import "context"

type runnerContextKey struct{}

func WithRunner(ctx context.Context, runner Runner) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		return ctx
	}
	return context.WithValue(ctx, runnerContextKey{}, runner)
}

func RunnerFromContext(ctx context.Context) Runner {
	if ctx == nil {
		return nil
	}
	runner, _ := ctx.Value(runnerContextKey{}).(Runner)
	return runner
}
