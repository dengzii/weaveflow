package core

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/dengzii/weaveflow/llms"
)

const DefaultModelID = "default"

type modelKey struct{}
type modelsKey struct{}
type modelConfigsKey struct{}
type toolsKey struct{}
type environmentKey struct{}

type Context struct {
	context.Context
}

type ModelConfig struct {
	ID        string
	Provider  string
	APIFormat string
	Model     string
	BaseURL   string
	ExtraBody map[string]any
	APIKey    string
	Pricing   llms.ModelPricing
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
	models := cloneModels(ModelsFromContext(ctx))
	if models == nil {
		models = map[string]llms.Model{}
	}
	if model != nil {
		models[DefaultModelID] = model
	} else {
		delete(models, DefaultModelID)
	}
	ctx = context.WithValue(ctx, modelsKey{}, models)
	return context.WithValue(ctx, modelKey{}, model)
}

func WithModels(ctx context.Context, available map[string]llms.Model) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	models := cloneModels(available)
	return context.WithValue(context.WithValue(ctx, modelsKey{}, models), modelKey{}, defaultModel(models))
}

func WithModelConfigs(ctx context.Context, available map[string]ModelConfig) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, modelConfigsKey{}, cloneModelConfigs(available))
}

func WithTools(ctx context.Context, available map[string]Tool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolsKey{}, cloneTools(available))
}

func WithEnvironment(ctx context.Context, environment map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, environmentKey{}, cloneEnvironment(environment))
}

func ModelFromContext(ctx context.Context) llms.Model {
	if ctx == nil {
		return nil
	}
	if model := ModelByIDFromContext(ctx, DefaultModelID); model != nil {
		return model
	}
	model, _ := ctx.Value(modelKey{}).(llms.Model)
	return model
}

func ModelByIDFromContext(ctx context.Context, id string) llms.Model {
	if ctx == nil {
		return nil
	}
	models := ModelsFromContext(ctx)
	id = strings.TrimSpace(id)
	if id == "" {
		if model, _ := ctx.Value(modelKey{}).(llms.Model); model != nil {
			return model
		}
		return defaultModel(models)
	}
	if len(models) == 0 {
		return nil
	}
	return models[id]
}

func ModelsFromContext(ctx context.Context) map[string]llms.Model {
	if ctx == nil {
		return nil
	}
	models, _ := ctx.Value(modelsKey{}).(map[string]llms.Model)
	return cloneModels(models)
}

func ModelConfigByIDFromContext(ctx context.Context, id string) (ModelConfig, bool) {
	configs := ModelConfigsFromContext(ctx)
	id = strings.TrimSpace(id)
	if id != "" {
		config, ok := configs[id]
		return config, ok
	}
	if config, ok := configs[DefaultModelID]; ok {
		return config, true
	}
	keys := make([]string, 0, len(configs))
	for key := range configs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ModelConfig{}, false
	}
	return configs[keys[0]], true
}

func ModelConfigsFromContext(ctx context.Context) map[string]ModelConfig {
	if ctx == nil {
		return nil
	}
	configs, _ := ctx.Value(modelConfigsKey{}).(map[string]ModelConfig)
	return cloneModelConfigs(configs)
}

func ToolsFromContext(ctx context.Context) map[string]Tool {
	if ctx == nil {
		return nil
	}
	available, _ := ctx.Value(toolsKey{}).(map[string]Tool)
	return cloneTools(available)
}

func EnvironmentFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	environment, _ := ctx.Value(environmentKey{}).(map[string]string)
	return cloneEnvironment(environment)
}

func EnvironmentVariableFromContext(ctx context.Context, name string) string {
	return EnvironmentFromContext(ctx)[strings.TrimSpace(name)]
}

func (c Context) Deadline() (time.Time, bool) {
	if c.Context == nil {
		return time.Time{}, false
	}
	return c.Context.Deadline()
}

func (c Context) Done() <-chan struct{} {
	if c.Context == nil {
		return nil
	}
	return c.Context.Done()
}

func (c Context) Err() error {
	if c.Context == nil {
		return nil
	}
	return c.Context.Err()
}

func (c Context) Value(key any) any {
	if c.Context == nil {
		return nil
	}
	return c.Context.Value(key)
}

func (c Context) Model(ids ...string) llms.Model {
	if len(ids) > 0 {
		return ModelByIDFromContext(c, ids[0])
	}
	return ModelFromContext(c)
}

func (c Context) Models() map[string]llms.Model {
	return ModelsFromContext(c)
}

func (c Context) ModelConfig(ids ...string) (ModelConfig, bool) {
	if len(ids) > 0 {
		return ModelConfigByIDFromContext(c, ids[0])
	}
	return ModelConfigByIDFromContext(c, "")
}

func (c Context) ModelConfigs() map[string]ModelConfig {
	return ModelConfigsFromContext(c)
}

func (c Context) Tools() map[string]Tool {
	return ToolsFromContext(c)
}

func (c Context) Environment() map[string]string {
	return EnvironmentFromContext(c)
}

func (c Context) FilterTools(ids []string) map[string]Tool {
	return FilterTools(c.Tools(), ids)
}

func FilterTools(available map[string]Tool, ids []string) map[string]Tool {
	if available == nil {
		return nil
	}
	if len(ids) == 0 {
		return cloneTools(available)
	}
	filtered := make(map[string]Tool, len(ids))
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

func cloneTools(input map[string]Tool) map[string]Tool {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]Tool, len(input))
	for key, tool := range input {
		id := strings.TrimSpace(key)
		if id == "" {
			continue
		}
		out[id] = tool
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneModels(input map[string]llms.Model) map[string]llms.Model {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]llms.Model, len(input))
	for key, model := range input {
		id := strings.TrimSpace(key)
		if id == "" || model == nil {
			continue
		}
		out[id] = model
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneModelConfigs(input map[string]ModelConfig) map[string]ModelConfig {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]ModelConfig, len(input))
	for key, config := range input {
		id := strings.TrimSpace(key)
		if id == "" {
			continue
		}
		config.ID = id
		config.Provider = strings.TrimSpace(config.Provider)
		config.APIFormat = strings.TrimSpace(config.APIFormat)
		config.Model = strings.TrimSpace(config.Model)
		config.BaseURL = strings.TrimSpace(config.BaseURL)
		config.APIKey = strings.TrimSpace(config.APIKey)
		if len(config.ExtraBody) > 0 {
			extraBody := make(map[string]any, len(config.ExtraBody))
			for field, value := range config.ExtraBody {
				extraBody[field] = value
			}
			config.ExtraBody = extraBody
		}
		out[id] = config
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneEnvironment(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func defaultModel(models map[string]llms.Model) llms.Model {
	if len(models) == 0 {
		return nil
	}
	if model := models[DefaultModelID]; model != nil {
		return model
	}
	keys := make([]string, 0, len(models))
	for key := range models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if models[key] != nil {
			return models[key]
		}
	}
	return nil
}
