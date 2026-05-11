package core

type ContractDiagnosticSeverity string

const (
	ContractDiagnosticSeverityError   ContractDiagnosticSeverity = "error"
	ContractDiagnosticSeverityWarning ContractDiagnosticSeverity = "warning"
)

type ContractDiagnostic struct {
	Severity    ContractDiagnosticSeverity `json:"severity"`
	Kind        string                     `json:"kind"`
	NodeID      string                     `json:"node_id,omitempty"`
	OtherNodeID string                     `json:"other_node_id,omitempty"`
	Path        string                     `json:"path,omitempty"`
	Sources     []string                   `json:"sources,omitempty"`
	Message     string                     `json:"message"`
}
