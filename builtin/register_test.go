package builtin

import "testing"

func TestDefaultRegistryExposesModulesCapabilitiesAndStatePorts(t *testing.T) {
	t.Parallel()
	reg := NewDefaultRegistry()
	module, ok := reg.StateModules[ProtocolsModuleName+"@"+ProtocolsModuleVersion]
	if !ok {
		t.Fatalf("protocol module is missing: %#v", reg.StateModules)
	}
	if len(module.Capabilities) != 4 {
		t.Fatalf("protocol capabilities = %#v", module.Capabilities)
	}
	for nodeType, definition := range reg.NodeTypes {
		if len(definition.StatePorts) == 0 {
			t.Fatalf("node type %q declares no state ports", nodeType)
		}
		if len(definition.NodeTypeSchema.StatePorts) != len(definition.StatePorts) {
			t.Fatalf("node type %q schema ports do not match build ports", nodeType)
		}
	}
	for conditionType, definition := range reg.Conditions {
		if len(definition.StatePorts) == 0 && definition.DynamicStatePorts == nil {
			t.Fatalf("condition %q declares no state ports", conditionType)
		}
		if len(definition.ConditionSchema.StatePorts) != len(definition.StatePorts) {
			t.Fatalf("condition %q schema ports do not match resolver ports", conditionType)
		}
	}
}
