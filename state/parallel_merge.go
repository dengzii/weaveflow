package state

import (
	"fmt"
	"sort"
	"strings"
)

// BranchPatch is one fan-in branch's state mutation.
//
// Order must be stable for the graph wave. The merger uses Order, NodeID, then
// input order to make append and merge application deterministic.
type BranchPatch struct {
	NodeID string
	Order  int
	Patch  Patch
}

type ParallelMergeOptions struct {
	Contracts map[string]Contract
}

// MergeParallelPatches applies fan-in branch patches to a shared base state in
// a deterministic order and rejects ambiguous overlapping writes.
func MergeParallelPatches(base *State, branches []BranchPatch, options ParallelMergeOptions) (*State, error) {
	target := NewState()
	if base != nil {
		target = base.Clone()
	}
	if len(branches) == 0 {
		return target, nil
	}

	entries, err := normalizeBranchPatchEntries(branches, options.Contracts)
	if err != nil {
		return nil, err
	}
	if err := validateParallelPatchConflicts(entries); err != nil {
		return nil, err
	}

	ops := make([]PatchOp, 0, len(entries))
	for _, entry := range entries {
		ops = append(ops, entry.op)
	}
	return NewPatch(ops...).Apply(target)
}

type parallelPatchEntry struct {
	nodeID     string
	branchOrd  int
	branchIdx  int
	opIdx      int
	op         PatchOp
	merge      MergeStrategy
	mergeKnown bool
}

func normalizeBranchPatchEntries(branches []BranchPatch, contracts map[string]Contract) ([]parallelPatchEntry, error) {
	entries := make([]parallelPatchEntry, 0)
	for branchIdx, branch := range branches {
		if contract, ok := contracts[branch.NodeID]; ok {
			if issues := ValidatePatchByContract(branch.Patch, contract); len(issues) > 0 {
				return nil, fmt.Errorf("branch %q patch contract violation: %s", branch.NodeID, issues[0].Message)
			}
		} else if issues := ValidatePatch(branch.Patch); len(issues) > 0 {
			return nil, fmt.Errorf("branch %q patch violation: %s", branch.NodeID, issues[0].Message)
		}
		for opIdx, op := range branch.Patch.Ops() {
			merge, known := mergeStrategyForPath(contracts[branch.NodeID], op.Path)
			entries = append(entries, parallelPatchEntry{
				nodeID:     branch.NodeID,
				branchOrd:  branch.Order,
				branchIdx:  branchIdx,
				opIdx:      opIdx,
				op:         op,
				merge:      merge,
				mergeKnown: known,
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.branchOrd != right.branchOrd {
			return left.branchOrd < right.branchOrd
		}
		if left.nodeID != right.nodeID {
			return left.nodeID < right.nodeID
		}
		if left.branchIdx != right.branchIdx {
			return left.branchIdx < right.branchIdx
		}
		return left.opIdx < right.opIdx
	})
	return entries, nil
}

func mergeStrategyForPath(contract Contract, path Path) (MergeStrategy, bool) {
	if path.Empty() {
		return MergeDefault, false
	}
	var (
		selected      MergeStrategy
		selectedDepth = -1
		found         bool
	)
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
			selected = field.Merge
			selectedDepth = depth
			found = true
		}
	}
	return selected, found
}

func validateParallelPatchConflicts(entries []parallelPatchEntry) error {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			left, right := entries[i], entries[j]
			if left.branchIdx == right.branchIdx {
				continue
			}
			if err := validateParallelPatchPair(left, right); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateParallelPatchPair(left, right parallelPatchEntry) error {
	leftPath := left.op.Path.String()
	rightPath := right.op.Path.String()
	if leftPath == "" || rightPath == "" {
		return nil
	}
	if leftPath != rightPath {
		if pathWithin(left.op.Path, right.op.Path) || pathWithin(right.op.Path, left.op.Path) {
			return parallelConflictError(left, right, "overlapping parent/child writes")
		}
		return nil
	}

	strategy, strategyKnown, err := parallelPairMergeStrategy(left, right)
	if err != nil {
		return err
	}
	if strategyKnown {
		return validateParallelPatchPairByStrategy(left, right, strategy)
	}

	if left.op.Kind == OpAppend && right.op.Kind == OpAppend {
		return nil
	}
	if left.op.Kind == OpMerge && right.op.Kind == OpMerge {
		if err := validateMergeOverlayConflict(left, right); err != nil {
			return err
		}
		return nil
	}
	if left.op.Kind == OpSet && right.op.Kind == OpSet && jsonEqual(left.op.Value, right.op.Value) {
		return nil
	}
	if left.op.Kind == OpDelete && right.op.Kind == OpDelete {
		return nil
	}
	return parallelConflictError(left, right, "conflicting writes")
}

func parallelPairMergeStrategy(left, right parallelPatchEntry) (MergeStrategy, bool, error) {
	leftStrategy, leftKnown := normalizedKnownMergeStrategy(left)
	rightStrategy, rightKnown := normalizedKnownMergeStrategy(right)
	if leftKnown && rightKnown && leftStrategy != rightStrategy {
		return MergeDefault, false, parallelConflictError(left, right, "incompatible merge strategies")
	}
	if leftKnown {
		return leftStrategy, true, nil
	}
	if rightKnown {
		return rightStrategy, true, nil
	}
	return MergeDefault, false, nil
}

func normalizedKnownMergeStrategy(entry parallelPatchEntry) (MergeStrategy, bool) {
	if !entry.mergeKnown || entry.merge == MergeDefault {
		return MergeDefault, false
	}
	return entry.merge, true
}

func validateParallelPatchPairByStrategy(left, right parallelPatchEntry, strategy MergeStrategy) error {
	switch strategy {
	case MergeAppend:
		if left.op.Kind == OpAppend && right.op.Kind == OpAppend {
			return nil
		}
		return parallelConflictError(left, right, "append merge strategy requires append ops")
	case MergeMerge:
		if left.op.Kind == OpMerge && right.op.Kind == OpMerge {
			return validateMergeOverlayConflict(left, right)
		}
		return parallelConflictError(left, right, "object merge strategy requires merge ops")
	case MergeReplace:
		if left.op.Kind == OpSet && right.op.Kind == OpSet && jsonEqual(left.op.Value, right.op.Value) {
			return nil
		}
		if left.op.Kind == OpDelete && right.op.Kind == OpDelete {
			return nil
		}
		return parallelConflictError(left, right, "replace merge strategy allows only identical replacements")
	default:
		return parallelConflictError(left, right, "unsupported merge strategy")
	}
}

func validateMergeOverlayConflict(left, right parallelPatchEntry) error {
	leftMap, _ := asMap(left.op.Value)
	rightMap, _ := asMap(right.op.Value)
	leftFlat := flattenMergeOverlay(leftMap)
	rightFlat := flattenMergeOverlay(rightMap)
	for leftPath, leftValue := range leftFlat {
		for rightPath, rightValue := range rightFlat {
			if leftPath == rightPath {
				if !jsonEqual(leftValue, rightValue) {
					return parallelConflictError(left, right, "conflicting merge values at "+leftPath)
				}
				continue
			}
			if dottedPathWithin(leftPath, rightPath) || dottedPathWithin(rightPath, leftPath) {
				return parallelConflictError(left, right, "overlapping merge values")
			}
		}
	}
	return nil
}

func flattenMergeOverlay(values map[string]any) map[string]any {
	out := map[string]any{}
	flattenMergeOverlayValue(out, "", values)
	return out
}

func flattenMergeOverlayValue(out map[string]any, prefix string, value any) {
	mapped, ok := value.(map[string]any)
	if !ok {
		out[prefix] = cloneValue(value)
		return
	}
	if len(mapped) == 0 {
		out[prefix] = map[string]any{}
		return
	}
	for key, item := range mapped {
		next := key
		if prefix != "" {
			next = prefix + "." + key
		}
		flattenMergeOverlayValue(out, next, item)
	}
}

func dottedPathWithin(path, parent string) bool {
	if path == "" || parent == "" {
		return false
	}
	return path != parent && strings.HasPrefix(path, parent+".")
}

func parallelConflictError(left, right parallelPatchEntry, reason string) error {
	return fmt.Errorf("parallel state merge conflict at %q between branches %q and %q: %s",
		left.op.Path.String(),
		left.nodeID,
		right.nodeID,
		reason,
	)
}
