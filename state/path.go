package state

import (
	"fmt"
	"strings"
)

const (
	SectionShared   = "shared"
	SectionScopes   = "scopes"
	SectionInternal = "internal"
	SectionRuntime  = "runtime"
)

// Path is the canonical address format used by state.
// Business code should construct paths through typed refs/accessors instead of
// parsing strings at call sites.
type Path struct {
	section  string
	segments []string
}

func NewPath(section string, segments ...string) (Path, error) {
	section = normalizeSegment(section)
	if section == "" {
		return Path{}, fmt.Errorf("state path section is required")
	}
	if !knownSection(section) {
		return Path{}, fmt.Errorf("unknown state path section %q", section)
	}

	cleaned := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = normalizeSegment(segment)
		if segment == "" {
			return Path{}, fmt.Errorf("state path segment is required")
		}
		if strings.Contains(segment, ".") {
			return Path{}, fmt.Errorf("state path segment %q must not contain '.'", segment)
		}
		cleaned = append(cleaned, segment)
	}
	return Path{section: section, segments: cleaned}, nil
}

func MustPath(section string, segments ...string) Path {
	path, err := NewPath(section, segments...)
	if err != nil {
		panic(err)
	}
	return path
}

func ParsePath(text string) (Path, error) {
	parts := strings.Split(strings.TrimSpace(text), ".")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return Path{}, fmt.Errorf("state path is required")
	}
	return NewPath(parts[0], parts[1:]...)
}

func Shared(segments ...string) Path {
	return MustPath(SectionShared, segments...)
}

func Runtime(segments ...string) Path {
	return MustPath(SectionRuntime, segments...)
}

func Internal(namespace string, segments ...string) Path {
	return MustPath(SectionInternal, append([]string{namespace}, segments...)...)
}

func Scope(scope string, segments ...string) Path {
	return MustPath(SectionScopes, append([]string{scope}, segments...)...)
}

func (p Path) Section() string {
	return p.section
}

func (p Path) Segments() []string {
	if len(p.segments) == 0 {
		return nil
	}
	return append([]string(nil), p.segments...)
}

func (p Path) Empty() bool {
	return p.section == ""
}

func (p Path) String() string {
	if p.section == "" {
		return ""
	}
	if len(p.segments) == 0 {
		return p.section
	}
	return p.section + "." + strings.Join(p.segments, ".")
}

func (p Path) Child(segments ...string) (Path, error) {
	all := append(p.Segments(), segments...)
	return NewPath(p.section, all...)
}

func (p Path) MustChild(segments ...string) Path {
	child, err := p.Child(segments...)
	if err != nil {
		panic(err)
	}
	return child
}

func knownSection(section string) bool {
	switch section {
	case SectionShared, SectionScopes, SectionInternal, SectionRuntime:
		return true
	default:
		return false
	}
}

func normalizeSegment(segment string) string {
	return strings.TrimSpace(segment)
}
