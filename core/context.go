package core

import (
	"context"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/memory"
	"github.com/dengzii/weaveflow/tools"

	"github.com/tmc/langchaingo/llms"
)

type modelKey struct{}
type toolsKey struct{}
type memoryKey struct{}

type Context struct {
	context.Context
}

func NewContext(ctx context.Context) Context {
	if ctx == nil {
		ctx = context.Background()
	}
	switch c := ctx.(type) {
	case Context:
		return c
	case *Context:
		if c != nil {
			return Context{Context: c.Context}
		}
	}
	return Context{Context: ctx}
}

func WithModel(ctx context.Context, model llms.Model) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modelKey{}, model)
}

func WithTools(ctx context.Context, available map[string]tools.Tool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolsKey{}, available)
}

func WithMemory(ctx context.Context, manager memory.Manager) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, memoryKey{}, manager)
}

func ModelFromContext(ctx context.Context) llms.Model {
	if ctx == nil {
		return nil
	}
	model, _ := ctx.Value(modelKey{}).(llms.Model)
	return model
}

func ToolsFromContext(ctx context.Context) map[string]tools.Tool {
	if ctx == nil {
		return nil
	}
	available, _ := ctx.Value(toolsKey{}).(map[string]tools.Tool)
	return available
}

func MemoryFromContext(ctx context.Context) memory.Manager {
	if ctx == nil {
		return nil
	}
	manager, _ := ctx.Value(memoryKey{}).(memory.Manager)
	return manager
}

func (c Context) Deadline() (time.Time, bool) { return c.Context.Deadline() }
func (c Context) Done() <-chan struct{}       { return c.Context.Done() }
func (c Context) Err() error                  { return c.Context.Err() }

func (c Context) Model() llms.Model {
	return ModelFromContext(c)
}

func (c Context) Tools() map[string]tools.Tool {
	return ToolsFromContext(c)
}

func (c Context) Memory() memory.Manager {
	return MemoryFromContext(c)
}

func (c Context) FilterTools(ids []string) map[string]tools.Tool {
	return FilterTools(c.Tools(), ids)
}

func FilterTools(available map[string]tools.Tool, ids []string) map[string]tools.Tool {
	if available == nil {
		return nil
	}
	if len(ids) == 0 {
		return available
	}
	filtered := make(map[string]tools.Tool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if tool, ok := available[id]; ok {
			filtered[id] = tool
		}
	}
	return filtered
}
