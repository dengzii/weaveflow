package codinggo

import "strings"

func NormalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}

	seen := make(map[string]struct{})
	result := make([]string, 0, len(tags))

	for _, tag := range tags {
		normalized := strings.TrimSpace(strings.ToLower(tag))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result
}
