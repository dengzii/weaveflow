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
