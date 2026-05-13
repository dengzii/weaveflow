package core

type ContractValidationMode string

const (
	ContractValidationOff    ContractValidationMode = ""
	ContractValidationWarn   ContractValidationMode = "warn"
	ContractValidationStrict ContractValidationMode = "strict"
)

type StateMergeStrategy string

const (
	StateMergeDefault StateMergeStrategy = ""
	StateMergeReplace StateMergeStrategy = "replace"
	StateMergeMerge   StateMergeStrategy = "merge"
	StateMergeAppend  StateMergeStrategy = "append"
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
	MergeStrategies    map[string]StateMergeStrategy
}

func (c NodeIOContract) IsEmpty() bool {
	return !c.WildcardRead &&
		!c.WildcardWrite &&
		len(c.ReadPaths) == 0 &&
		len(c.WritePaths) == 0 &&
		len(c.RequiredReadPaths) == 0 &&
		len(c.RequiredWritePaths) == 0 &&
		len(c.MergeStrategies) == 0
}

func (c NodeIOContract) Clone() NodeIOContract {
	cloned := c
	cloned.ReadPaths = cloneStringSlice(c.ReadPaths)
	cloned.WritePaths = cloneStringSlice(c.WritePaths)
	cloned.RequiredReadPaths = cloneStringSlice(c.RequiredReadPaths)
	cloned.RequiredWritePaths = cloneStringSlice(c.RequiredWritePaths)
	if len(c.MergeStrategies) > 0 {
		cloned.MergeStrategies = make(map[string]StateMergeStrategy, len(c.MergeStrategies))
		for path, strategy := range c.MergeStrategies {
			cloned.MergeStrategies[path] = strategy
		}
	}
	return cloned
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
