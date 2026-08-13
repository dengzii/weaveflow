package state

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// SectionShared stores graph-wide business state.
	SectionShared = "shared"
	// SectionScopes stores state namespaced by node or agent scope.
	SectionScopes = "scopes"
	// SectionInternal is reserved for framework internals.
	SectionInternal = "internal"
	// SectionRuntime stores checkpoint/runtime metadata.
	SectionRuntime = "runtime"
)

// Path is the canonical address format used by state.
// Business code should construct paths through typed refs or capability views instead of
// parsing strings at call sites.
type Path struct {
	section  string
	segments []string
}

// NewPath constructs a validated state path from a known section and clean
// segments. Segments may not contain dots.
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

// MustPath is NewPath for package-level declarations; it panics on invalid
// input.
func MustPath(section string, segments ...string) Path {
	path, err := NewPath(section, segments...)
	if err != nil {
		panic(err)
	}
	return path
}

// ParsePath parses the dotted form used in DSL and event payloads, for example
// "shared.request.input".
func ParsePath(text string) (Path, error) {
	parts := strings.Split(strings.TrimSpace(text), ".")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return Path{}, fmt.Errorf("state path is required")
	}
	return NewPath(parts[0], parts[1:]...)
}

// Shared constructs a path in the shared business state section.
func Shared(segments ...string) Path {
	return MustPath(SectionShared, segments...)
}

// Runtime constructs a path in the runtime metadata section.
func Runtime(segments ...string) Path {
	return MustPath(SectionRuntime, segments...)
}

// Internal constructs a path in the internal section under namespace.
func Internal(namespace string, segments ...string) Path {
	return MustPath(SectionInternal, append([]string{namespace}, segments...)...)
}

// Scope constructs a path in the scoped section under scope.
func Scope(scope string, segments ...string) Path {
	return MustPath(SectionScopes, append([]string{scope}, segments...)...)
}

// Section returns the root section of the path.
func (p Path) Section() string {
	return p.section
}

// Segments returns a copy of path segments after the section.
func (p Path) Segments() []string {
	if len(p.segments) == 0 {
		return nil
	}
	return append([]string(nil), p.segments...)
}

// Empty reports whether path has no section and cannot address state.
func (p Path) Empty() bool {
	return p.section == ""
}

// String returns the canonical dotted path.
func (p Path) String() string {
	if p.section == "" {
		return ""
	}
	if len(p.segments) == 0 {
		return p.section
	}
	return p.section + "." + strings.Join(p.segments, ".")
}

// MarshalJSON encodes a path as its canonical dotted string.
func (p Path) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *Path) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("state path target is nil")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParsePath(text)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// Child appends segments to an existing path and validates them.
func (p Path) Child(segments ...string) (Path, error) {
	all := append(p.Segments(), segments...)
	return NewPath(p.section, all...)
}

// MustChild is Child for package-level ref declarations; it panics on invalid
// input.
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
