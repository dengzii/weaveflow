package weaveflow

import (
	"fmt"
	"sort"
	"strings"
	"weaveflow/core"
	fruntime "weaveflow/runtime"
)

type ContractDiagnosticSeverity = core.ContractDiagnosticSeverity

const (
	ContractDiagnosticSeverityError   = core.ContractDiagnosticSeverityError
	ContractDiagnosticSeverityWarning = core.ContractDiagnosticSeverityWarning
)

type ContractDiagnostic = core.ContractDiagnostic

func (g *Graph) ContractDiagnostics() []ContractDiagnostic {
	if g == nil || len(g.contractDiagnostics) == 0 {
		return nil
	}
	cloned := make([]ContractDiagnostic, len(g.contractDiagnostics))
	for i, diagnostic := range g.contractDiagnostics {
		cloned[i] = diagnostic
		if len(diagnostic.Sources) > 0 {
			cloned[i].Sources = append([]string(nil), diagnostic.Sources...)
		}
	}
	return cloned
}

func initialContractPathsFromStateFields(fields map[string]StateFieldDefinition) []string {
	if len(fields) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(fields))
	paths := make([]string, 0, len(fields))
	for name := range fields {
		path := fruntime.NormalizeContractPath(name)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func analyzeContractDiagnostics(g *Graph) []ContractDiagnostic {
	if g == nil || len(g.nodeContracts) == 0 || g.entryPoint == "" {
		return nil
	}

	reachable := reachableGraphNodes(g)
	if len(reachable) == 0 {
		return nil
	}

	predecessors := graphPredecessors(g, reachable)
	ancestors := graphAncestors(reachable, predecessors)

	diagnostics := make([]ContractDiagnostic, 0)
	diagnostics = append(diagnostics, wildcardContractDiagnostics(g, reachable)...)
	diagnostics = append(diagnostics, overlappingWriteDiagnostics(g, reachable)...)
	diagnostics = append(diagnostics, requiredReadDiagnostics(g, reachable, ancestors)...)

	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i]
		right := diagnostics[j]
		if left.Severity != right.Severity {
			return left.Severity == ContractDiagnosticSeverityError
		}
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		if left.OtherNodeID != right.OtherNodeID {
			return left.OtherNodeID < right.OtherNodeID
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Kind < right.Kind
	})
	return diagnostics
}

func contractDiagnosticsError(diagnostics []ContractDiagnostic) error {
	errors := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != ContractDiagnosticSeverityError {
			continue
		}
		errors = append(errors, diagnostic.Message)
	}
	if len(errors) == 0 {
		return nil
	}
	return fmt.Errorf("graph contract validation failed:\n- %s", strings.Join(errors, "\n- "))
}

func reachableGraphNodes(g *Graph) []string {
	if g == nil || g.entryPoint == "" {
		return nil
	}

	visited := map[string]struct{}{}
	queue := []string{g.entryPoint}
	order := make([]string, 0, len(g.nodes))
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if _, ok := visited[nodeID]; ok {
			continue
		}
		if _, ok := g.nodes[nodeID]; !ok {
			continue
		}
		visited[nodeID] = struct{}{}
		order = append(order, nodeID)

		if next, ok := g.edges[nodeID]; ok && next != EndNodeRef && next != "" {
			queue = append(queue, next)
		}
		for _, edge := range g.conditionalEdges[nodeID] {
			if edge.to == EndNodeRef || edge.to == "" {
				continue
			}
			queue = append(queue, edge.to)
		}
	}
	sort.Strings(order)
	return order
}

func graphPredecessors(g *Graph, reachable []string) map[string][]string {
	reachableSet := make(map[string]struct{}, len(reachable))
	for _, nodeID := range reachable {
		reachableSet[nodeID] = struct{}{}
	}

	predecessors := make(map[string][]string, len(reachable))
	for _, nodeID := range reachable {
		predecessors[nodeID] = nil
	}

	addPredecessor := func(from, to string) {
		if _, ok := reachableSet[from]; !ok {
			return
		}
		if _, ok := reachableSet[to]; !ok {
			return
		}
		predecessors[to] = append(predecessors[to], from)
	}

	for from, to := range g.edges {
		addPredecessor(from, to)
	}
	for from, edges := range g.conditionalEdges {
		for _, edge := range edges {
			addPredecessor(from, edge.to)
		}
	}

	for nodeID, items := range predecessors {
		if len(items) == 0 {
			continue
		}
		sort.Strings(items)
		items = compactStrings(items)
		predecessors[nodeID] = items
	}
	return predecessors
}

func graphAncestors(reachable []string, predecessors map[string][]string) map[string]map[string]struct{} {
	ancestors := make(map[string]map[string]struct{}, len(reachable))
	for _, nodeID := range reachable {
		ancestors[nodeID] = map[string]struct{}{}
	}

	changed := true
	for changed {
		changed = false
		for _, nodeID := range reachable {
			target := ancestors[nodeID]
			for _, predecessor := range predecessors[nodeID] {
				if predecessor != nodeID {
					if _, ok := target[predecessor]; !ok {
						target[predecessor] = struct{}{}
						changed = true
					}
				}
				for ancestor := range ancestors[predecessor] {
					if ancestor == nodeID {
						continue
					}
					if _, ok := target[ancestor]; !ok {
						target[ancestor] = struct{}{}
						changed = true
					}
				}
			}
		}
	}
	return ancestors
}

func wildcardContractDiagnostics(g *Graph, reachable []string) []ContractDiagnostic {
	diagnostics := make([]ContractDiagnostic, 0)
	for _, nodeID := range reachable {
		contract, ok := g.nodeContracts[nodeID]
		if !ok {
			continue
		}
		if !contract.WildcardRead && !contract.WildcardWrite {
			continue
		}
		parts := make([]string, 0, 2)
		if contract.WildcardRead {
			parts = append(parts, "read")
		}
		if contract.WildcardWrite {
			parts = append(parts, "write")
		}
		diagnostics = append(diagnostics, ContractDiagnostic{
			Severity: ContractDiagnosticSeverityWarning,
			Kind:     "wildcard_contract",
			NodeID:   nodeID,
			Message:  fmt.Sprintf("node %q uses wildcard %s contract access, which weakens static dependency analysis", nodeID, strings.Join(parts, "/")),
		})
	}
	return diagnostics
}

func overlappingWriteDiagnostics(g *Graph, reachable []string) []ContractDiagnostic {
	diagnostics := make([]ContractDiagnostic, 0)
	for i := 0; i < len(reachable); i++ {
		leftID := reachable[i]
		left, ok := g.nodeContracts[leftID]
		if !ok {
			continue
		}
		for j := i + 1; j < len(reachable); j++ {
			rightID := reachable[j]
			right, ok := g.nodeContracts[rightID]
			if !ok {
				continue
			}
			overlapPath, ok := overlappingWritePath(left, right)
			if !ok {
				continue
			}
			diagnostics = append(diagnostics, ContractDiagnostic{
				Severity:    ContractDiagnosticSeverityWarning,
				Kind:        "overlapping_write",
				NodeID:      leftID,
				OtherNodeID: rightID,
				Path:        overlapPath,
				Message:     fmt.Sprintf("nodes %q and %q both write overlapping path %q", leftID, rightID, overlapPath),
			})
		}
	}
	return diagnostics
}

func overlappingWritePath(left, right fruntime.NodeIOContract) (string, bool) {
	if left.WildcardWrite || right.WildcardWrite {
		return "*", true
	}
	for _, leftPath := range left.WritePaths {
		for _, rightPath := range right.WritePaths {
			if leftPath == rightPath {
				return leftPath, true
			}
			if strings.HasPrefix(leftPath, rightPath+".") {
				return rightPath, true
			}
			if strings.HasPrefix(rightPath, leftPath+".") {
				return leftPath, true
			}
		}
	}
	return "", false
}

func requiredReadDiagnostics(g *Graph, reachable []string, ancestors map[string]map[string]struct{}) []ContractDiagnostic {
	diagnostics := make([]ContractDiagnostic, 0)
	for _, nodeID := range reachable {
		contract, ok := g.nodeContracts[nodeID]
		if !ok || len(contract.RequiredReadPaths) == 0 || contract.WildcardRead {
			continue
		}

		required := append([]string(nil), contract.RequiredReadPaths...)
		sort.Strings(required)
		required = compactStrings(required)

		for _, path := range required {
			sources := requiredReadSources(g, nodeID, path, ancestors[nodeID])
			if len(sources) == 0 {
				diagnostics = append(diagnostics, ContractDiagnostic{
					Severity: ContractDiagnosticSeverityError,
					Kind:     "missing_required_read",
					NodeID:   nodeID,
					Path:     path,
					Message:  fmt.Sprintf("node %q requires input path %q but no initial input or upstream writer can provide it", nodeID, path),
				})
				continue
			}
			if len(sources) > 1 {
				diagnostics = append(diagnostics, ContractDiagnostic{
					Severity: ContractDiagnosticSeverityWarning,
					Kind:     "multiple_read_sources",
					NodeID:   nodeID,
					Path:     path,
					Sources:  sources,
					Message:  fmt.Sprintf("node %q can read required path %q from multiple sources: %s", nodeID, path, strings.Join(sources, ", ")),
				})
			}
		}
	}
	return diagnostics
}

func requiredReadSources(g *Graph, nodeID string, path string, ancestors map[string]struct{}) []string {
	sources := make([]string, 0)
	for _, initialPath := range g.initialStatePaths {
		if sourceProvidesRead(initialPath, path) {
			sources = append(sources, "input")
			break
		}
	}

	if contract, ok := g.nodeContracts[nodeID]; ok && selfRuntimePathProvidesRead(nodeID, contract, path) {
		sources = append(sources, nodeID)
	}

	ancestorIDs := make([]string, 0, len(ancestors))
	for ancestorID := range ancestors {
		ancestorIDs = append(ancestorIDs, ancestorID)
	}
	sort.Strings(ancestorIDs)
	for _, ancestorID := range ancestorIDs {
		contract, ok := g.nodeContracts[ancestorID]
		if !ok {
			continue
		}
		if contractProvidesRead(contract, path) {
			sources = append(sources, ancestorID)
		}
	}

	if len(sources) == 0 && pathMayBeProvidedByInitialState(path) {
		sources = append(sources, "input")
	}

	return compactStrings(sources)
}

func selfRuntimePathProvidesRead(nodeID string, contract fruntime.NodeIOContract, path string) bool {
	runtimePrefix := "runtime." + strings.TrimSpace(nodeID)
	if path != runtimePrefix && !strings.HasPrefix(path, runtimePrefix+".") {
		return false
	}
	return contractProvidesReadWriteSource(contract, path)
}

func contractProvidesRead(contract fruntime.NodeIOContract, path string) bool {
	if contract.WildcardWrite {
		return true
	}
	return contractProvidesReadWriteSource(contract, path)
}

func contractProvidesReadWriteSource(contract fruntime.NodeIOContract, path string) bool {
	for _, writePath := range contract.WritePaths {
		if sourceProvidesRead(writePath, path) {
			return true
		}
	}
	return false
}

func sourceProvidesRead(sourcePath string, readPath string) bool {
	sourcePath = strings.TrimSpace(sourcePath)
	readPath = strings.TrimSpace(readPath)
	if sourcePath == "" || readPath == "" {
		return false
	}
	if sourcePath == "*" {
		return true
	}
	return sourcePath == readPath || strings.HasPrefix(readPath, sourcePath+".")
}

func pathMayBeProvidedByInitialState(path string) bool {
	path = strings.TrimSpace(path)
	switch {
	case path == "", path == "*":
		return false
	case path == "shared" || strings.HasPrefix(path, "shared."):
		return true
	case path == "conversation" || strings.HasPrefix(path, "conversation."):
		return true
	case strings.HasPrefix(path, "scopes."):
		return true
	case path == "internal" || strings.HasPrefix(path, "internal."):
		return true
	default:
		return false
	}
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	var last string
	for _, value := range values {
		if value == "" {
			continue
		}
		if len(result) == 0 || value != last {
			result = append(result, value)
			last = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
