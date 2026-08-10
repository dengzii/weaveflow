package codex

import "context"

type codexRunnerContextKey struct{}

func WithRunner(ctx context.Context, runner Runner) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		return ctx
	}
	return context.WithValue(ctx, codexRunnerContextKey{}, runner)
}

func RunnerFromContext(ctx context.Context) Runner {
	if ctx == nil {
		return nil
	}
	runner, _ := ctx.Value(codexRunnerContextKey{}).(Runner)
	return runner
}
