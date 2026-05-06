package neo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	fruntime "weaveflow/runtime"
)

type RunMetadata struct {
	RunID         string         `json:"run_id,omitempty"`
	GraphID       string         `json:"graph_id,omitempty"`
	GraphVersion  string         `json:"graph_version,omitempty"`
	Status        string         `json:"status,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	Request       ChatRequest    `json:"request"`
	Config        Config         `json:"config"`
	EnabledTools  []string       `json:"enabled_tools,omitempty"`
	InitialState  fruntime.State `json:"initial_state,omitempty"`
	FinalState    fruntime.State `json:"final_state,omitempty"`
	FinalAnswer   string         `json:"final_answer,omitempty"`
	Error         string         `json:"error,omitempty"`
	GraphFile     string         `json:"graph_file,omitempty"`
	ExecutionRoot string         `json:"execution_root,omitempty"`
}

func enabledToolNames(flags map[string]bool) []string {
	names := make([]string, 0, len(flags))
	for name, enabled := range flags {
		if enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func writeRunMetadata(runDir string, meta RunMetadata) error {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "run.json"), data, 0o644)
}
