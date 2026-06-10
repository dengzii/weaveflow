package core

import (
	"context"
	"strings"
	"time"
	"weaveflow/memory"
	"weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type Services struct {
	Model  llms.Model
	Tools  map[string]tools.Tool
	Memory memory.Manager
}

type servicesKey struct{}

type Context struct {
	context.Context
	services *Services
}

func NewContext(ctx context.Context, svc *Services) Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if svc == nil {
		switch c := ctx.(type) {
		case Context:
			svc = c.services
			if c.Context != nil {
				ctx = c.Context
			}
		case *Context:
			if c != nil {
				svc = c.services
				if c.Context != nil {
					ctx = c.Context
				}
			}
		}
	}
	if svc == nil {
		svc, _ = ctx.Value(servicesKey{}).(*Services)
	}
	return Context{Context: ctx, services: svc}
}

func (c Context) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c Context) Done() <-chan struct{}       { return c.Context.Done() }
func (c Context) Err() error                  { return c.Context.Err() }
func (c Context) Value(key any) any {
	if key == (servicesKey{}) && c.services != nil {
		return c.services
	}
	return c.Context.Value(key)
}

func (c Context) Model() llms.Model {
	if c.services == nil {
		return nil
	}
	return c.services.Model
}

func (c Context) Tools() map[string]tools.Tool {
	if c.services == nil {
		return nil
	}
	return c.services.Tools
}

func (c Context) Memory() memory.Manager {
	if c.services == nil {
		return nil
	}
	return c.services.Memory
}

func (c Context) FilterTools(ids []string) map[string]tools.Tool {
	if c.services == nil {
		return nil
	}
	return c.services.FilterTools(ids)
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
