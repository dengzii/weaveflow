package runtime

import (
	"context"
	wfctx "weaveflow/context"
)

type Services = wfctx.Services

func WithServices(ctx context.Context, svc *Services) context.Context {
	return wfctx.WithServices(ctx, svc)
}

func ServicesFrom(ctx context.Context) *Services {
	return wfctx.ServicesFrom(ctx)
}
