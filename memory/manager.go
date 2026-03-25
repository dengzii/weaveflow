package memory

import (
	"strings"
	"time"
)

type memoryManager struct {
	options *Options
	repo    Repository
}

func (m *memoryManager) Store(memory []Entry) error {
	if m == nil || m.repo == nil {
		return nil
	}

	return m.repo.Store(cloneEntries(memory))
}

func (m *memoryManager) Append(memory ...Entry) error {
	if m == nil || m.repo == nil {
		return nil
	}

	if len(memory) == 0 {
		return nil
	}

	items, err := m.Load(nil)
	if err != nil {
		return err
	}

	for _, entry := range memory {
		items = append(items, normalizeEntry(entry))
	}

	return m.Store(items)
}

func (m *memoryManager) Load(options *LoadOptions) ([]Entry, error) {
	if m == nil || m.repo == nil {
		return []Entry{}, nil
	}

	return m.repo.Load(options)
}

func (m *memoryManager) Recall(query *Query) ([]Entry, error) {
	if m == nil {
		return []Entry{}, nil
	}

	if m.options != nil && m.options.Retriever != nil {
		return m.options.Retriever.Recall(query)
	}

	if query == nil {
		return m.Load(nil)
	}

	return m.Load(&LoadOptions{
		Limit: query.Limit,
		Roles: query.Roles,
		Since: query.Since,
		Until: query.Until,
	})
}

func (m *memoryManager) Delete() error {
	if m == nil || m.repo == nil {
		return nil
	}

	return m.repo.Delete()
}

func cloneEntries(entries []Entry) []Entry {
	if len(entries) == 0 {
		return []Entry{}
	}

	cloned := make([]Entry, len(entries))
	for i, entry := range entries {
		cloned[i] = normalizeEntry(entry)
	}
	return cloned
}

func filterEntries(entries []Entry, options *LoadOptions) []Entry {
	if len(entries) == 0 {
		return []Entry{}
	}
	if options == nil {
		return cloneEntries(entries)
	}

	roleSet := normalizeRoleSet(options.Roles)
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if len(roleSet) > 0 {
			if _, ok := roleSet[strings.TrimSpace(entry.Role)]; !ok {
				continue
			}
		}
		if !options.Since.IsZero() && memoryTime(entry).Before(options.Since) {
			continue
		}
		if !options.Until.IsZero() && memoryTime(entry).After(options.Until) {
			continue
		}
		filtered = append(filtered, entry)
	}

	if options.Limit > 0 && len(filtered) > options.Limit {
		filtered = filtered[:options.Limit]
	}

	return cloneEntries(filtered)
}

func normalizeRoleSet(roles []string) map[string]struct{} {
	if len(roles) == 0 {
		return nil
	}

	roleSet := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		roleSet[role] = struct{}{}
	}

	return roleSet
}

func memoryTime(entry Entry) time.Time {
	return entry.CreatedAt
}
