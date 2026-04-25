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

type NodeWriteContract struct {
	WritePaths    []string
	RequiredPaths []string
	Wildcard      bool
}

func ValidateNodeContract(
	nodeID string,
	contract NodeWriteContract,
	afterState State,
	changes []StateChange,
) []ContractViolation {
	if contract.Wildcard || (len(contract.WritePaths) == 0 && len(contract.RequiredPaths) == 0) {
		return nil
	}

	var violations []ContractViolation

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

	for _, path := range contract.RequiredPaths {
		businessPath := snapshotPathToBusinessPath(path)
		if businessPath == "" {
			continue
		}
		if _, found := ResolveStatePath(afterState, businessPath); !found {
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

func snapshotPathToBusinessPath(snapshotPath string) string {
	if strings.HasPrefix(snapshotPath, "shared.") {
		return strings.TrimPrefix(snapshotPath, "shared.")
	}
	if strings.HasPrefix(snapshotPath, "scopes.") {
		return snapshotPath
	}
	return ""
}

func isRuntimeOrConversationPath(path string) bool {
	return path == "runtime" ||
		strings.HasPrefix(path, "runtime.") ||
		path == "conversation" ||
		strings.HasPrefix(path, "conversation.")
}
