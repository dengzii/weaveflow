package graphbuild

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"
	"github.com/dengzii/weaveflow/dsl"
	"github.com/dengzii/weaveflow/state"
)

type ContractAnalysisGraph struct {
	EntryPoint        string
	EndNode           string
	InitialStatePaths []string
	Edges             map[string][]string
	ConditionalEdges  map[string][]string
	NodeContracts     map[string]state.Contract
}

func AnalyzeContractDiagnostics(input ContractAnalysisGraph) []core.ContractDiagnostic {
	if len(input.NodeContracts) == 0 || input.EntryPoint == "" {
		return nil
	}

	reachable := reachableGraphNodes(input)
	if len(reachable) == 0 {
		return nil
	}

	predecessors := graphPredecessors(input, reachable)
	ancestors := graphAncestors(reachable, predecessors)

	diagnostics := make([]core.ContractDiagnostic, 0)
	diagnostics = append(diagnostics, wildcardContractDiagnostics(input, reachable)...)
	diagnostics = append(diagnostics, overlappingWriteDiagnostics(input, reachable)...)
	diagnostics = append(diagnostics, requiredReadDiagnostics(input, reachable, ancestors)...)

	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i]
		right := diagnostics[j]
		if left.Severity != right.Severity {
			return left.Severity == core.ContractDiagnosticSeverityError
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

func AnalyzeInitialStateRequirements(input ContractAnalysisGraph) core.InitialStateRequirements {
	result := emptyInitialStateRequirements()
	if len(input.NodeContracts) == 0 || input.EntryPoint == "" {
		return result
	}

	reachable := reachableGraphNodes(input)
	if len(reachable) == 0 {
		return result
	}

	predecessors := graphPredecessors(input, reachable)
	ancestors := graphAncestors(reachable, predecessors)
	required := map[string]*initialStateRequirementAccumulator{}
	provided := map[string]*initialStateRequirementAccumulator{}
	unresolved := map[string]*initialStateRequirementAccumulator{}

	for _, nodeID := range reachable {
		contract, ok := input.NodeContracts[nodeID]
		if !ok || contract.WildcardRead {
			continue
		}
		for _, field := range requiredReadFields(contract) {
			path := field.Path.String()
			sources := requiredReadSources(input, nodeID, path, ancestors[nodeID])
			graphSources := nonInputSources(sources)
			switch {
			case len(sources) == 0:
				addInitialStateRequirement(unresolved, path, nodeID, nil, field, fmt.Sprintf("node %q requires input path %q but no initial input or upstream writer can provide it", nodeID, path))
			case len(graphSources) > 0:
				addInitialStateRequirement(provided, path, nodeID, graphSources, field, "")
			default:
				addInitialStateRequirement(required, path, nodeID, nil, field, "")
			}
		}
	}

	result.Required = initialStateRequirementList(required)
	result.ProvidedByUpstream = initialStateRequirementList(provided)
	result.Unresolved = initialStateRequirementList(unresolved)
	result.Warnings = contractAnalysisWarnings(input)
	return result
}

func emptyInitialStateRequirements() core.InitialStateRequirements {
	return core.InitialStateRequirements{
		Required:           []core.InitialStateRequirement{},
		ProvidedByUpstream: []core.InitialStateRequirement{},
		Unresolved:         []core.InitialStateRequirement{},
	}
}

type initialStateRequirementAccumulator struct {
	path        string
	nodes       map[string]struct{}
	sources     map[string]struct{}
	fieldType   string
	description string
	message     string
}

func addInitialStateRequirement(target map[string]*initialStateRequirementAccumulator, path, nodeID string, sources []string, field state.FieldAccess, message string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	item := target[path]
	if item == nil {
		item = &initialStateRequirementAccumulator{
			path:    path,
			nodes:   map[string]struct{}{},
			sources: map[string]struct{}{},
		}
		target[path] = item
	}
	if nodeID != "" {
		item.nodes[nodeID] = struct{}{}
	}
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source != "" {
			item.sources[source] = struct{}{}
		}
	}
	if item.fieldType == "" {
		item.fieldType = strings.TrimSpace(field.Type)
	}
	if item.description == "" {
		item.description = strings.TrimSpace(field.Description)
	}
	if item.message == "" {
		item.message = strings.TrimSpace(message)
	}
}

func initialStateRequirementList(input map[string]*initialStateRequirementAccumulator) []core.InitialStateRequirement {
	if len(input) == 0 {
		return []core.InitialStateRequirement{}
	}
	paths := make([]string, 0, len(input))
	for path := range input {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	out := make([]core.InitialStateRequirement, 0, len(paths))
	for _, path := range paths {
		item := input[path]
		out = append(out, core.InitialStateRequirement{
			Path:        item.path,
			Nodes:       sortedStringSet(item.nodes),
			Sources:     sortedStringSet(item.sources),
			Type:        item.fieldType,
			Description: item.description,
			Message:     item.message,
		})
	}
	return out
}

func sortedStringSet(input map[string]struct{}) []string {
	if len(input) == 0 {
		return nil
	}
	values := make([]string, 0, len(input))
	for value := range input {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func nonInputSources(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source) == "input" {
			continue
		}
		out = append(out, source)
	}
	return compactStrings(out)
}

func contractAnalysisWarnings(input ContractAnalysisGraph) []core.ContractDiagnostic {
	diagnostics := AnalyzeContractDiagnostics(input)
	if len(diagnostics) == 0 {
		return nil
	}
	warnings := make([]core.ContractDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == core.ContractDiagnosticSeverityWarning {
			warnings = append(warnings, diagnostic)
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func ContractDiagnosticsError(diagnostics []core.ContractDiagnostic) error {
	errors := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != core.ContractDiagnosticSeverityError {
			continue
		}
		errors = append(errors, diagnostic.Message)
	}
	if len(errors) == 0 {
		return nil
	}
	return fmt.Errorf("graph contract validation failed:\n- %s", strings.Join(errors, "\n- "))
}

func InitialContractPathsFromStateFields(fields map[string]dsl.StateFieldDefinition) []string {
	if len(fields) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(fields))
	paths := make([]string, 0, len(fields))
	for name := range fields {
		path := stateFieldInitialPath(name)
		if strings.TrimSpace(path) == "" {
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

func stateFieldInitialPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if parsed, err := state.ParsePath(name); err == nil {
		return parsed.String()
	}
	if strings.Contains(name, ".") {
		return ""
	}
	return state.Shared(name).String()
}

func reachableGraphNodes(input ContractAnalysisGraph) []string {
	if input.EntryPoint == "" {
		return nil
	}

	visited := map[string]struct{}{}
	queue := []string{input.EntryPoint}
	order := make([]string, 0, len(input.NodeContracts))
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if _, ok := visited[nodeID]; ok {
			continue
		}
		visited[nodeID] = struct{}{}
		order = append(order, nodeID)

		for _, next := range input.Edges[nodeID] {
			if !isAnalysisEndTarget(input, next) {
				queue = append(queue, next)
			}
		}
		for _, to := range input.ConditionalEdges[nodeID] {
			if isAnalysisEndTarget(input, to) {
				continue
			}
			queue = append(queue, to)
		}
	}
	sort.Strings(order)
	return order
}

func graphPredecessors(input ContractAnalysisGraph, reachable []string) map[string][]string {
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

	for from, targets := range input.Edges {
		for _, to := range targets {
			addPredecessor(from, to)
		}
	}
	for from, edges := range input.ConditionalEdges {
		for _, to := range edges {
			addPredecessor(from, to)
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

func wildcardContractDiagnostics(input ContractAnalysisGraph, reachable []string) []core.ContractDiagnostic {
	diagnostics := make([]core.ContractDiagnostic, 0)
	for _, nodeID := range reachable {
		contract, ok := input.NodeContracts[nodeID]
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
		diagnostics = append(diagnostics, core.ContractDiagnostic{
			Severity: core.ContractDiagnosticSeverityWarning,
			Kind:     "wildcard_contract",
			NodeID:   nodeID,
			Message:  fmt.Sprintf("node %q uses wildcard %s contract access, which weakens static dependency analysis", nodeID, strings.Join(parts, "/")),
		})
	}
	return diagnostics
}

func overlappingWriteDiagnostics(input ContractAnalysisGraph, reachable []string) []core.ContractDiagnostic {
	diagnostics := make([]core.ContractDiagnostic, 0)
	waves := analysisParallelWaves(input, reachable)
	for _, wave := range waves {
		for i := 0; i < len(wave); i++ {
			leftID := wave[i]
			left, ok := input.NodeContracts[leftID]
			if !ok {
				continue
			}
			for j := i + 1; j < len(wave); j++ {
				rightID := wave[j]
				right, ok := input.NodeContracts[rightID]
				if !ok {
					continue
				}
				overlapPath, ok := overlappingWritePath(left, right)
				if !ok {
					continue
				}
				diagnostics = append(diagnostics, core.ContractDiagnostic{
					Severity:    core.ContractDiagnosticSeverityWarning,
					Kind:        "overlapping_write",
					NodeID:      leftID,
					OtherNodeID: rightID,
					Path:        overlapPath,
					Message:     fmt.Sprintf("parallel branches %q and %q both write overlapping path %q", leftID, rightID, overlapPath),
				})
			}
		}
	}
	return diagnostics
}

func analysisParallelWaves(input ContractAnalysisGraph, reachable []string) [][]string {
	reachableSet := make(map[string]struct{}, len(reachable))
	for _, nodeID := range reachable {
		reachableSet[nodeID] = struct{}{}
	}

	waves := make([][]string, 0)
	addWave := func(targets []string) {
		if len(targets) <= 1 {
			return
		}
		seen := map[string]struct{}{}
		wave := make([]string, 0, len(targets))
		for _, target := range targets {
			if isAnalysisEndTarget(input, target) {
				continue
			}
			if _, ok := reachableSet[target]; !ok {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			wave = append(wave, target)
		}
		if len(wave) <= 1 {
			return
		}
		waves = append(waves, wave)
	}

	for _, from := range reachable {
		if len(input.ConditionalEdges[from]) > 0 {
			continue
		}
		addWave(input.Edges[from])
	}
	return waves
}

func overlappingWritePath(left, right state.Contract) (string, bool) {
	if left.WildcardWrite || right.WildcardWrite {
		return "*", true
	}
	for _, leftPath := range pathStrings(left.WritePaths()) {
		for _, rightPath := range pathStrings(right.WritePaths()) {
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

func requiredReadDiagnostics(input ContractAnalysisGraph, reachable []string, ancestors map[string]map[string]struct{}) []core.ContractDiagnostic {
	diagnostics := make([]core.ContractDiagnostic, 0)
	for _, nodeID := range reachable {
		contract, ok := input.NodeContracts[nodeID]
		requiredReads := requiredReadPaths(contract)
		if !ok || len(requiredReads) == 0 || contract.WildcardRead {
			continue
		}

		required := append([]string(nil), requiredReads...)
		sort.Strings(required)
		required = compactStrings(required)

		for _, path := range required {
			sources := requiredReadSources(input, nodeID, path, ancestors[nodeID])
			if len(sources) == 0 {
				diagnostics = append(diagnostics, core.ContractDiagnostic{
					Severity: core.ContractDiagnosticSeverityError,
					Kind:     "missing_required_read",
					NodeID:   nodeID,
					Path:     path,
					Message:  fmt.Sprintf("node %q requires input path %q but no initial input or upstream writer can provide it", nodeID, path),
				})
				continue
			}
			if len(sources) > 1 {
				diagnostics = append(diagnostics, core.ContractDiagnostic{
					Severity: core.ContractDiagnosticSeverityWarning,
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

func requiredReadSources(input ContractAnalysisGraph, nodeID string, path string, ancestors map[string]struct{}) []string {
	sources := make([]string, 0)
	for _, initialPath := range input.InitialStatePaths {
		if sourceProvidesRead(initialPath, path) {
			sources = append(sources, "input")
			break
		}
	}

	if contract, ok := input.NodeContracts[nodeID]; ok && selfRuntimePathProvidesRead(nodeID, contract, path) {
		sources = append(sources, nodeID)
	}

	ancestorIDs := make([]string, 0, len(ancestors))
	for ancestorID := range ancestors {
		ancestorIDs = append(ancestorIDs, ancestorID)
	}
	sort.Strings(ancestorIDs)
	for _, ancestorID := range ancestorIDs {
		contract, ok := input.NodeContracts[ancestorID]
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

func selfRuntimePathProvidesRead(nodeID string, contract state.Contract, path string) bool {
	runtimePrefix := "runtime." + strings.TrimSpace(nodeID)
	if path != runtimePrefix && !strings.HasPrefix(path, runtimePrefix+".") {
		return false
	}
	return contractProvidesReadWriteSource(contract, path)
}

func contractProvidesRead(contract state.Contract, path string) bool {
	if contract.WildcardWrite {
		return true
	}
	return contractProvidesReadWriteSource(contract, path)
}

func contractProvidesReadWriteSource(contract state.Contract, path string) bool {
	for _, writePath := range pathStrings(contract.WritePaths()) {
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

func isAnalysisEndTarget(input ContractAnalysisGraph, target string) bool {
	if target == "" {
		return true
	}
	if input.EndNode != "" {
		return target == input.EndNode
	}
	return target == "__end__"
}

func requiredReadPaths(contract state.Contract) []string {
	paths := make([]string, 0)
	for _, field := range requiredReadFields(contract) {
		paths = append(paths, field.Path.String())
	}
	return compactStrings(paths)
}

func requiredReadFields(contract state.Contract) []state.FieldAccess {
	fields := make([]state.FieldAccess, 0)
	for _, field := range contract.Fields {
		if !field.Required || field.Path.Empty() {
			continue
		}
		if field.Mode != state.AccessRead && field.Mode != state.AccessReadWrite {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func pathStrings(paths []state.Path) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if !path.Empty() {
			out = append(out, path.String())
		}
	}
	return out
}
