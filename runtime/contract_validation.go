package runtime

import (
	"fmt"
	"strings"
)

type ContractValidationMode string

const (
	ContractValidationOff    ContractValidationMode = ""
	ContractValidationWarn   ContractValidationMode = "warn"
	ContractValidationStrict ContractValidationMode = "strict"
)

type ContractViolation struct {
	NodeID  string `json:"node_id"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type NodeIOContract struct {
	ReadPaths          []string
	WritePaths         []string
	RequiredReadPaths  []string
	RequiredWritePaths []string
	WildcardRead       bool
	WildcardWrite      bool
}

func (c NodeIOContract) IsEmpty() bool {
	return !c.WildcardRead &&
		!c.WildcardWrite &&
		len(c.ReadPaths) == 0 &&
		len(c.WritePaths) == 0 &&
		len(c.RequiredReadPaths) == 0 &&
		len(c.RequiredWritePaths) == 0
}

func ValidateNodeInputContract(
	nodeID string,
	contract NodeIOContract,
	inputState State,
) []ContractViolation {
	if contract.WildcardRead || len(contract.RequiredReadPaths) == 0 {
		return nil
	}

	violations := make([]ContractViolation, 0, len(contract.RequiredReadPaths))
	for _, path := range contract.RequiredReadPaths {
		if _, found := resolveSnapshotPathValue(inputState, path); found {
			continue
		}
		violations = append(violations, ContractViolation{
			NodeID:  nodeID,
			Path:    path,
			Kind:    "missing_required_read",
			Message: fmt.Sprintf("node %q requires input path %q but it was not found in the state", nodeID, path),
		})
	}
	return violations
}

func ValidateNodeContract(
	nodeID string,
	contract NodeIOContract,
	afterState State,
	changes []StateChange,
) []ContractViolation {
	if contract.IsEmpty() {
		return nil
	}

	var violations []ContractViolation

	if !contract.WildcardWrite {
		for _, change := range changes {
			if isRuntimeOrConversationPath(change.Path) {
				continue
			}
			if !isPathCoveredByContract(change.Path, contract.WritePaths) {
				violations = append(violations, ContractViolation{
					NodeID:  nodeID,
					Path:    change.Path,
					Kind:    "undeclared_write",
					Message: fmt.Sprintf("node %q wrote to path %q not declared as writable in its state contract", nodeID, change.Path),
				})
			}
		}
	}

	for _, path := range contract.RequiredWritePaths {
		if _, found := resolveSnapshotPathValue(afterState, path); !found {
			violations = append(violations, ContractViolation{
				NodeID:  nodeID,
				Path:    path,
				Kind:    "missing_required",
				Message: fmt.Sprintf("node %q must write path %q but it was not found in the output state", nodeID, path),
			})
		}
	}

	return violations
}

func isPathCoveredByContract(changePath string, writePaths []string) bool {
	for _, wp := range writePaths {
		if changePath == wp {
			return true
		}
		if strings.HasPrefix(changePath, wp+".") {
			return true
		}
		if strings.HasPrefix(wp, changePath+".") {
			return true
		}
	}
	return false
}

func NormalizeContractPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "*" {
		return path
	}
	if strings.HasPrefix(path, "scopes.") ||
		strings.HasPrefix(path, "internal.") ||
		path == "runtime" || strings.HasPrefix(path, "runtime.") ||
		path == "conversation" || strings.HasPrefix(path, "conversation.") ||
		path == "artifacts" {
		return path
	}
	if strings.HasPrefix(path, StateNamespacePrefix) {
		return "internal." + path
	}
	return "shared." + path
}

func isRuntimeOrConversationPath(path string) bool {
	return path == "runtime" ||
		strings.HasPrefix(path, "runtime.") ||
		path == "conversation" ||
		strings.HasPrefix(path, "conversation.")
}
