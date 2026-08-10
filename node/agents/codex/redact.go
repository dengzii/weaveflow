package codex

import "strings"

type secretRedactor struct {
	replacer *strings.Replacer
}

func newSecretRedactor(values []string) secretRedactor {
	replacements := make([]string, 0, len(values)*2)
	seen := map[string]struct{}{}
	for _, value := range values {
		if len(value) < 4 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		replacements = append(replacements, value, "[REDACTED]")
	}
	if len(replacements) == 0 {
		return secretRedactor{}
	}
	return secretRedactor{replacer: strings.NewReplacer(replacements...)}
}

func (redactor secretRedactor) text(value string) string {
	if redactor.replacer == nil || value == "" {
		return value
	}
	return redactor.replacer.Replace(value)
}
