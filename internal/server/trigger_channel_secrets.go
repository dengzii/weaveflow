package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dengzii/weaveflow/internal/chatchannel"
	"github.com/dengzii/weaveflow/internal/trigger"
)

func (s *Server) externalizeChatChannelSecrets(ctx context.Context, item *trigger.Trigger) (func(bool), error) {
	noSecrets := func(bool) {}
	if item == nil || item.Type != trigger.TypeChat || item.Chat == nil {
		return noSecrets, nil
	}
	if s == nil || s.chatChannels == nil || s.managedSecrets == nil {
		return nil, fmt.Errorf("chat channel secret storage is unavailable")
	}

	releases := make([]func(bool), 0)
	config, err := s.chatChannels.MapWriteOnlyConfig(item.Chat.Channel, item.Chat.ChannelConfig, func(path string, value any) (any, error) {
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text == "" {
				return nil, nil
			}
			ref, release, err := s.managedSecrets.Put(ctx, text)
			if err != nil {
				return nil, fmt.Errorf("store chat channel config %q: %w", path, err)
			}
			releases = append(releases, release)
			return ref, nil
		}

		ref, err := chatchannel.ParseSecretRef(value)
		if err != nil {
			return nil, invalidRequestf("chat channel config %q: %v", path, err)
		}
		if ref.Source == managedSecretSource {
			return nil, invalidRequestf("chat channel config %q: unsupported secret source %q", path, ref.Source)
		}
		normalized, err := normalizeSecretRef(ref)
		if err != nil {
			return nil, invalidRequestf("chat channel config %q: %v", path, err)
		}
		return normalized, nil
	})
	if err != nil {
		for _, release := range releases {
			release(false)
		}
		if errors.Is(err, chatchannel.ErrChannelNotFound) {
			return nil, invalidRequestf("%v", err)
		}
		return nil, err
	}
	item.Chat.ChannelConfig = config

	var once sync.Once
	releaseAll := func(commit bool) {
		once.Do(func() {
			for _, release := range releases {
				release(commit)
			}
		})
	}
	return releaseAll, nil
}

func (s *Server) sweepManagedSecrets(ctx context.Context) error {
	if s == nil || s.managedSecrets == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.triggers == nil || s.chatChannels == nil {
		return fmt.Errorf("chat channel secret storage is unavailable")
	}
	referenced := make(map[string]struct{})
	if err := s.triggers.InspectDefinitions(ctx, func(items []trigger.Trigger) error {
		for _, item := range items {
			if item.Type != trigger.TypeChat || item.Chat == nil {
				continue
			}
			_, err := s.chatChannels.MapWriteOnlyConfig(item.Chat.Channel, item.Chat.ChannelConfig, func(path string, value any) (any, error) {
				ref, err := chatchannel.ParseSecretRef(value)
				if err != nil {
					return nil, fmt.Errorf("trigger %q chat channel config %q: %w", item.ID, path, err)
				}
				if ref.Source != managedSecretSource {
					return value, nil
				}
				if !isManagedSecretID(ref.Ref) {
					return nil, fmt.Errorf("trigger %q chat channel config %q: managed secret ref is invalid", item.ID, path)
				}
				referenced[ref.Ref] = struct{}{}
				return value, nil
			})
			if err != nil {
				return fmt.Errorf("scan managed secrets: %w", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("inspect triggers for managed secret cleanup: %w", err)
	}
	if err := s.managedSecrets.sweep(ctx, referenced); err != nil {
		return fmt.Errorf("sweep managed secrets: %w", err)
	}
	return nil
}
