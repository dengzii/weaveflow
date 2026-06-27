package core

// InitialStateRequirements describes which required state reads must be
// supplied by the graph's initial state and which can be satisfied by the
// graph itself.
type InitialStateRequirements struct {
	Required           []InitialStateRequirement `json:"required"`
	ProvidedByUpstream []InitialStateRequirement `json:"provided_by_upstream"`
	Unresolved         []InitialStateRequirement `json:"unresolved"`
	Warnings           []ContractDiagnostic      `json:"warnings,omitempty"`
}

// InitialStateRequirement groups required read paths by state path. Nodes are
// the readers that need the path; Sources are graph nodes that can provide it
// when the entry is reported under ProvidedByUpstream.
type InitialStateRequirement struct {
	Path        string   `json:"path"`
	Nodes       []string `json:"nodes,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Message     string   `json:"message,omitempty"`
}
