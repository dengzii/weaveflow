package state

// AccessMode describes how a node uses a state field.
type AccessMode string

const (
	// AccessRead declares that a node reads the field.
	AccessRead AccessMode = "read"
	// AccessWrite declares that a node writes the field.
	AccessWrite AccessMode = "write"
	// AccessReadWrite declares that a node both reads and writes the field.
	AccessReadWrite AccessMode = "read_write"
)

// MergeStrategy describes how concurrent branch writes should be reconciled.
type MergeStrategy string

const (
	// MergeDefault lets the merge engine infer behavior from patch operations.
	MergeDefault MergeStrategy = ""
	// MergeReplace allows only identical concurrent replacements.
	MergeReplace MergeStrategy = "replace"
	// MergeMerge allows object merge operations with disjoint keys.
	MergeMerge MergeStrategy = "merge"
	// MergeAppend allows append operations to be ordered deterministically.
	MergeAppend MergeStrategy = "append"
)

// FieldAccess declares one path in a node contract.
type FieldAccess struct {
	Path        Path
	Mode        AccessMode
	Required    bool
	Merge       MergeStrategy
	Reducer     string
	Type        string
	Schema      JSONSchema
	Description string
}

// Contract declares the state paths a node may read or write. It is used for
// input projection, write validation, and parallel merge conflict handling.
type Contract struct {
	Fields        []FieldAccess
	WildcardRead  bool
	WildcardWrite bool
}

// NewContract constructs a contract from cloned field declarations.
func NewContract(fields ...FieldAccess) Contract {
	return Contract{Fields: cloneFieldAccess(fields)}
}

// Clone returns an independent copy of the contract.
func (c Contract) Clone() Contract {
	c.Fields = cloneFieldAccess(c.Fields)
	return c
}

// ReadPaths returns unique paths declared readable by the contract.
func (c Contract) ReadPaths() []Path {
	return contractPaths(c.Fields, func(mode AccessMode) bool {
		return mode == AccessRead || mode == AccessReadWrite
	})
}

// WritePaths returns unique paths declared writable by the contract.
func (c Contract) WritePaths() []Path {
	return contractPaths(c.Fields, func(mode AccessMode) bool {
		return mode == AccessWrite || mode == AccessReadWrite
	})
}

func cloneFieldAccess(fields []FieldAccess) []FieldAccess {
	if len(fields) == 0 {
		return nil
	}
	cloned := make([]FieldAccess, len(fields))
	for index, field := range fields {
		cloned[index] = field
		cloned[index].Schema = field.Schema.Clone()
	}
	return cloned
}

func contractPaths(fields []FieldAccess, include func(AccessMode) bool) []Path {
	if len(fields) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	paths := make([]Path, 0, len(fields))
	for _, field := range fields {
		if field.Path.Empty() || !include(field.Mode) {
			continue
		}
		key := field.Path.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, field.Path)
	}
	return paths
}
