package state

import "fmt"

type ValidationIssue struct {
	Path    string `json:"path,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func ValidateContract(contract Contract) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	for _, field := range contract.Fields {
		path := field.Path.String()
		if field.Path.Empty() {
			issues = append(issues, ValidationIssue{
				Kind:    "empty_contract_path",
				Message: "contract field path is required",
			})
		}
		if !validAccessMode(field.Mode) {
			issues = append(issues, ValidationIssue{
				Path:    path,
				Kind:    "invalid_access_mode",
				Message: fmt.Sprintf("invalid access mode %q", field.Mode),
			})
		}
		if !validMergeStrategy(field.Merge) {
			issues = append(issues, ValidationIssue{
				Path:    path,
				Kind:    "invalid_merge_strategy",
				Message: fmt.Sprintf("invalid merge strategy %q", field.Merge),
			})
		}
	}
	return issues
}

func ValidateRequiredReads(state *State, contract Contract) []ValidationIssue {
	issues := ValidateContract(contract)
	if state == nil {
		state = NewState()
	}
	for _, field := range contract.Fields {
		if !field.Required || !isReadMode(field.Mode) || field.Path.Empty() {
			continue
		}
		if _, ok := state.read(field.Path); ok {
			continue
		}
		issues = append(issues, ValidationIssue{
			Path:    field.Path.String(),
			Kind:    "missing_required_read",
			Message: fmt.Sprintf("required read path %q is missing", field.Path.String()),
		})
	}
	return issues
}

func ProjectStateByContract(full *State, contract Contract) *State {
	if full == nil {
		full = NewState()
	}
	if contract.WildcardRead {
		return full.Clone()
	}

	projected := NewState()
	for _, path := range contract.ReadPaths() {
		if path.Empty() {
			continue
		}
		value, ok := full.read(path)
		if !ok {
			continue
		}
		_ = projected.set(path, value)
	}
	return projected
}

func ValidatePatch(patch Patch) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	for _, op := range patch.Ops() {
		path := op.Path.String()
		if op.Path.Empty() {
			issues = append(issues, ValidationIssue{
				Kind:    "empty_patch_path",
				Message: "patch op path is required",
			})
		}
		if !validPatchOpKind(op.Kind) {
			issues = append(issues, ValidationIssue{
				Path:    path,
				Kind:    "invalid_patch_op",
				Message: fmt.Sprintf("invalid patch op kind %q", op.Kind),
			})
		}
		if op.Kind == OpMerge {
			if _, ok := asMap(op.Value); !ok {
				issues = append(issues, ValidationIssue{
					Path:    path,
					Kind:    "invalid_merge_value",
					Message: fmt.Sprintf("merge op at %q requires map[string]any value, got %T", path, op.Value),
				})
			}
		}
	}
	return issues
}

func ValidatePatchByContract(patch Patch, contract Contract) []ValidationIssue {
	issues := ValidatePatch(patch)
	issues = append(issues, ValidateContract(contract)...)
	if contract.WildcardWrite {
		return issues
	}

	writePaths := contract.WritePaths()
	for _, op := range patch.Ops() {
		if op.Path.Empty() {
			continue
		}
		if pathAllowedByAny(op.Path, writePaths) {
			continue
		}
		issues = append(issues, ValidationIssue{
			Path:    op.Path.String(),
			Kind:    "write_not_allowed",
			Message: fmt.Sprintf("patch writes undeclared path %q", op.Path.String()),
		})
	}

	for _, field := range contract.Fields {
		if !field.Required || !isWriteMode(field.Mode) || field.Path.Empty() {
			continue
		}
		if patchCoversPath(patch, field.Path) {
			continue
		}
		issues = append(issues, ValidationIssue{
			Path:    field.Path.String(),
			Kind:    "missing_required_write",
			Message: fmt.Sprintf("patch must write required path %q", field.Path.String()),
		})
	}
	return issues
}

func validAccessMode(mode AccessMode) bool {
	switch mode {
	case AccessRead, AccessWrite, AccessReadWrite:
		return true
	default:
		return false
	}
}

func validMergeStrategy(strategy MergeStrategy) bool {
	switch strategy {
	case MergeDefault, MergeReplace, MergeMerge, MergeAppend:
		return true
	default:
		return false
	}
}

func validPatchOpKind(kind PatchOpKind) bool {
	switch kind {
	case OpSet, OpDelete, OpMerge, OpAppend:
		return true
	default:
		return false
	}
}

func isReadMode(mode AccessMode) bool {
	return mode == AccessRead || mode == AccessReadWrite
}

func isWriteMode(mode AccessMode) bool {
	return mode == AccessWrite || mode == AccessReadWrite
}

func pathAllowedByAny(path Path, allowed []Path) bool {
	for _, candidate := range allowed {
		if pathWithin(path, candidate) {
			return true
		}
	}
	return false
}

func patchCoversPath(patch Patch, required Path) bool {
	for _, op := range patch.Ops() {
		if pathWithin(required, op.Path) {
			return true
		}
	}
	return false
}

func pathWithin(path Path, parent Path) bool {
	if path.Empty() || parent.Empty() || path.section != parent.section {
		return false
	}
	if len(path.segments) < len(parent.segments) {
		return false
	}
	for i, segment := range parent.segments {
		if path.segments[i] != segment {
			return false
		}
	}
	return true
}
