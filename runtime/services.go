package runtime

import (
	"context"
	"strings"
	"weaveflow/memory"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type servicesKey struct{}

type Services struct {
	Model  llms.Model
	Tools  map[string]tools.Tool
	Memory memory.Manager
}

func WithServices(ctx context.Context, svc *Services) context.Context {
	if ctx == nil || svc == nil {
		return ctx
	}
	return context.WithValue(ctx, servicesKey{}, svc)
}

func ServicesFrom(ctx context.Context) *Services {
	if ctx == nil {
		return nil
	}
	svc, _ := ctx.Value(servicesKey{}).(*Services)
	return svc
}

func (s *Services) FilterTools(ids []string) map[string]tools.Tool {
	if s == nil || s.Tools == nil {
		return nil
	}
	if len(ids) == 0 {
		return s.Tools
	}
	filtered := make(map[string]tools.Tool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if tool, ok := s.Tools[id]; ok {
			filtered[id] = tool
		}
	}
	return filtered
}
