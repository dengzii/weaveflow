package core

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
