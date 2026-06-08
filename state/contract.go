package state

type AccessMode string

const (
	AccessRead      AccessMode = "read"
	AccessWrite     AccessMode = "write"
	AccessReadWrite AccessMode = "read_write"
)

type MergeStrategy string

const (
	MergeDefault MergeStrategy = ""
	MergeReplace MergeStrategy = "replace"
	MergeMerge   MergeStrategy = "merge"
	MergeAppend  MergeStrategy = "append"
)

type FieldAccess struct {
	Path        Path
	Mode        AccessMode
	Required    bool
	Merge       MergeStrategy
	Type        string
	Description string
}

type Contract struct {
	Fields        []FieldAccess
	WildcardRead  bool
	WildcardWrite bool
}

func NewContract(fields ...FieldAccess) Contract {
	return Contract{Fields: cloneFieldAccess(fields)}
}

func (c Contract) Clone() Contract {
	c.Fields = cloneFieldAccess(c.Fields)
	return c
}

func (c Contract) ReadPaths() []Path {
	return contractPaths(c.Fields, func(mode AccessMode) bool {
		return mode == AccessRead || mode == AccessReadWrite
	})
}

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
	copy(cloned, fields)
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
