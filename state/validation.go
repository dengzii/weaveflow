package state

import (
	"fmt"
	"sort"
	"strings"
)

type ValidationIssue struct {
	Path    string `json:"path,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type ValidationError struct {
	Boundary string            `json:"boundary"`
	Issues   []ValidationIssue `json:"issues"`
}

func NewValidationError(boundary string, issues []ValidationIssue) *ValidationError {
	if len(issues) == 0 {
		return nil
	}
	cloned := append([]ValidationIssue(nil), issues...)
	sortValidationIssues(cloned)
	return &ValidationError{Boundary: strings.TrimSpace(boundary), Issues: cloned}
}

func (validationErr *ValidationError) Error() string {
	if validationErr == nil || len(validationErr.Issues) == 0 {
		return "state validation failed"
	}
	prefix := "state validation failed"
	if validationErr.Boundary != "" {
		prefix = validationErr.Boundary + " state validation failed"
	}
	first := validationErr.Issues[0]
	if first.Path != "" {
		return fmt.Sprintf("%s at %q: %s", prefix, first.Path, first.Message)
	}
	return fmt.Sprintf("%s: %s", prefix, first.Message)
}

func ValidateStateBySchemas(currentState *State, schemas map[string]JSONSchema) []ValidationIssue {
	if len(schemas) == 0 {
		return nil
	}
	issues := make([]ValidationIssue, 0)
	if currentState == nil {
		currentState = NewState()
	}
	paths := make([]string, 0, len(schemas))
	for path := range schemas {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, pathText := range paths {
		path, err := ParsePath(pathText)
		if err != nil {
			issues = append(issues, ValidationIssue{Path: pathText, Kind: "invalid_schema_path", Message: err.Error()})
			continue
		}
		value, exists := currentState.read(path)
		if !exists {
			continue
		}
		issues = append(issues, ValidateJSONSchemaValue(value, schemas[pathText], pathText)...)
	}
	return issues
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
		if err := ValidateJSONSchemaDefinition(field.Schema); err != nil {
			issues = append(issues, ValidationIssue{
				Path:    path,
				Kind:    "invalid_json_schema",
				Message: fmt.Sprintf("invalid JSON Schema at %q: %v", path, err),
			})
		}
	}
	return issues
}

func ValidateRequiredReads(current *State, contract Contract) []ValidationIssue {
	issues := ValidateContract(contract)
	if current == nil {
		current = NewState()
	}
	for _, field := range contract.Fields {
		if !isReadMode(field.Mode) || field.Path.Empty() {
			continue
		}
		value, ok := current.read(field.Path)
		if ok {
			issues = append(issues, ValidateJSONSchemaValue(value, field.Schema, field.Path.String())...)
			continue
		}
		if !field.Required {
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
		if op.Kind == OpReduce && strings.TrimSpace(op.Reducer) == "" {
			issues = append(issues, ValidationIssue{Path: path, Kind: "missing_reducer", Message: "reduce op requires reducer"})
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
			reducer, known := reducerForPath(contract, op.Path)
			switch {
			case known && reducer != "" && (op.Kind != OpReduce || strings.TrimSpace(op.Reducer) != reducer):
				issues = append(issues, ValidationIssue{
					Path:    op.Path.String(),
					Kind:    "reducer_mismatch",
					Message: fmt.Sprintf("patch at %q must use reducer %q", op.Path.String(), reducer),
				})
			case known && reducer == "" && op.Kind == OpReduce:
				issues = append(issues, ValidationIssue{
					Path:    op.Path.String(),
					Kind:    "reducer_not_declared",
					Message: fmt.Sprintf("patch at %q uses reducer %q without declaring it in the state contract", op.Path.String(), op.Reducer),
				})
			}
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

func reducerForPath(contract Contract, path Path) (string, bool) {
	if path.Empty() {
		return "", false
	}
	selected := ""
	selectedDepth := -1
	found := false
	for _, field := range contract.Fields {
		if !isWriteMode(field.Mode) || field.Path.Empty() {
			continue
		}
		if !pathWithin(path, field.Path) && !pathWithin(field.Path, path) {
			continue
		}
		depth := len(field.Path.segments)
		if field.Path.section != "" {
			depth++
		}
		if depth > selectedDepth {
			selected = strings.TrimSpace(field.Reducer)
			selectedDepth = depth
			found = true
		}
	}
	return selected, found
}

func ValidatePatchResultByContract(base *State, patch Patch, contract Contract) []ValidationIssue {
	return ValidatePatchResultByContractWithReducers(base, patch, contract, nil)
}

func ValidatePatchResultByContractWithReducers(base *State, patch Patch, contract Contract, reducers map[string]Reducer) []ValidationIssue {
	issues := ValidatePatchByContract(patch, contract)
	resultState, err := patch.ApplyWithReducers(base, reducers)
	if err != nil {
		issues = append(issues, ValidationIssue{Kind: "patch_apply_failed", Message: err.Error()})
		sortValidationIssues(issues)
		return issues
	}
	writtenPaths := make(map[string]struct{})
	for _, op := range patch.Ops() {
		if !op.Path.Empty() {
			writtenPaths[op.Path.String()] = struct{}{}
		}
	}
	for _, field := range contract.Fields {
		if !isWriteMode(field.Mode) || field.Path.Empty() || len(field.Schema) == 0 {
			continue
		}
		if !pathAffectedByWrites(field.Path, writtenPaths) {
			continue
		}
		value, exists := resultState.read(field.Path)
		if !exists {
			if field.Required {
				issues = append(issues, ValidationIssue{
					Path:    field.Path.String(),
					Kind:    "missing_required_write_value",
					Message: fmt.Sprintf("required write path %q is missing after patch", field.Path.String()),
				})
			}
			continue
		}
		issues = append(issues, ValidateJSONSchemaValue(value, field.Schema, field.Path.String())...)
	}
	sortValidationIssues(issues)
	return issues
}

func ValidateInputPatchByContractWithReducers(base *State, patch Patch, contract Contract, reducers map[string]Reducer) []ValidationIssue {
	issues := ValidatePatch(patch)
	readPaths := contract.ReadPaths()
	if !contract.WildcardRead {
		for _, op := range patch.Ops() {
			if op.Path.Empty() {
				continue
			}
			if pathAllowedByAny(op.Path, readPaths) {
				reducer, known := reducerForPath(contract, op.Path)
				switch {
				case known && reducer != "" && (op.Kind != OpReduce || strings.TrimSpace(op.Reducer) != reducer):
					issues = append(issues, ValidationIssue{
						Path:    op.Path.String(),
						Kind:    "reducer_mismatch",
						Message: fmt.Sprintf("input patch at %q must use reducer %q", op.Path.String(), reducer),
					})
				case op.Kind == OpReduce && (!known || reducer == ""):
					issues = append(issues, ValidationIssue{
						Path:    op.Path.String(),
						Kind:    "reducer_not_declared",
						Message: fmt.Sprintf("input patch at %q uses reducer %q without declaring it in the writable state contract", op.Path.String(), op.Reducer),
					})
				}
				continue
			}
			issues = append(issues, ValidationIssue{
				Path:    op.Path.String(),
				Kind:    "input_not_allowed",
				Message: fmt.Sprintf("input patch writes unreadable path %q", op.Path.String()),
			})
		}
	}
	inputState, err := patch.ApplyWithReducers(base, reducers)
	if err != nil {
		issues = append(issues, ValidationIssue{Kind: "input_patch_apply_failed", Message: err.Error()})
	} else {
		issues = append(issues, ValidateRequiredReads(inputState, contract)...)
	}
	sortValidationIssues(issues)
	return issues
}

func pathAffectedByWrites(path Path, writtenPaths map[string]struct{}) bool {
	for writtenPathText := range writtenPaths {
		writtenPath, err := ParsePath(writtenPathText)
		if err != nil {
			continue
		}
		if pathWithin(path, writtenPath) || pathWithin(writtenPath, path) {
			return true
		}
	}
	return false
}

func sortValidationIssues(issues []ValidationIssue) {
	sort.SliceStable(issues, func(leftIndex, rightIndex int) bool {
		if issues[leftIndex].Path != issues[rightIndex].Path {
			return issues[leftIndex].Path < issues[rightIndex].Path
		}
		if issues[leftIndex].Kind != issues[rightIndex].Kind {
			return issues[leftIndex].Kind < issues[rightIndex].Kind
		}
		return issues[leftIndex].Message < issues[rightIndex].Message
	})
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
	case OpSet, OpDelete, OpMerge, OpAppend, OpReduce:
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
		if path.String() == candidate.String() {
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
