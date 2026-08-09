package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGraphInstanceConfigRequiresCurrentVersion(t *testing.T) {
	t.Parallel()
	valid := GraphInstanceConfig{Version: GraphInstanceConfigVersion, ID: "local", GraphRef: "graph"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, version := range []string{"", "0.9"} {
		config := valid
		config.Version = version
		if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "version must be") {
			t.Fatalf("Validate() version %q error = %v", version, err)
		}
	}
}

func TestRunRequestRequiresCurrentVersion(t *testing.T) {
	t.Parallel()
	valid := RunRequest{Version: RunRequestVersion, InstanceID: "local"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, version := range []string{"", "0.9"} {
		request := valid
		request.Version = version
		if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "version must be") {
			t.Fatalf("Validate() version %q error = %v", version, err)
		}
	}
}

func TestRuntimeDefinitionsRejectUnknownFields(t *testing.T) {
	t.Parallel()
	if _, err := DeserializeGraphInstanceConfig([]byte(`{"version":"1.0","id":"local","graph_ref":"graph","legacy":true}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DeserializeGraphInstanceConfig() error = %v", err)
	}
	if _, err := DeserializeRunRequest([]byte(`{"version":"1.0","instance_id":"local","legacy":true}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DeserializeRunRequest() error = %v", err)
	}
}

func TestRunRequestBreakpointJSONRoundTrip(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"version":"1.0","instance_id":"local","debug":{"breakpoints":[{"id":"approval","node_id":"review","stage":"after_node","enabled":false}]}}`)
	request, err := DeserializeRunRequest(payload)
	if err != nil {
		t.Fatalf("DeserializeRunRequest() error = %v", err)
	}
	if request.Debug == nil || len(request.Debug.Breakpoints) != 1 {
		t.Fatalf("debug breakpoints = %#v", request.Debug)
	}
	breakpoint := request.Debug.Breakpoints[0]
	if breakpoint.ID != "approval" || breakpoint.NodeID != "review" || breakpoint.Stage != DebugBreakpointAfterNode || breakpoint.Enabled == nil || *breakpoint.Enabled {
		t.Fatalf("breakpoint = %#v", breakpoint)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"breakpoints":[{"id":"approval","node_id":"review","stage":"after_node","enabled":false}]`) {
		t.Fatalf("encoded request = %s", encoded)
	}
}
