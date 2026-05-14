package runtime

import (
	"testing"

	"weaveflow/core"
)

func TestContractPolicyForModeDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		mode              core.ContractValidationMode
		enabled           bool
		enforceProjection bool
		enforceWrites     bool
		recordArtifacts   bool
	}{
		{
			name:    "off",
			mode:    core.ContractValidationOff,
			enabled: false,
		},
		{
			name:              "warn",
			mode:              core.ContractValidationWarn,
			enabled:           true,
			enforceProjection: true,
			recordArtifacts:   true,
		},
		{
			name:              "strict",
			mode:              core.ContractValidationStrict,
			enabled:           true,
			enforceProjection: true,
			enforceWrites:     true,
			recordArtifacts:   true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := ContractPolicyForMode(tc.mode)
			if policy.Mode != tc.mode {
				t.Fatalf("Mode = %q, want %q", policy.Mode, tc.mode)
			}
			if policy.Enabled() != tc.enabled {
				t.Fatalf("Enabled() = %v, want %v", policy.Enabled(), tc.enabled)
			}
			if policy.EnforceProjection != tc.enforceProjection {
				t.Fatalf("EnforceProjection = %v, want %v", policy.EnforceProjection, tc.enforceProjection)
			}
			if policy.EnforceWrites != tc.enforceWrites {
				t.Fatalf("EnforceWrites = %v, want %v", policy.EnforceWrites, tc.enforceWrites)
			}
			if policy.RecordArtifacts != tc.recordArtifacts {
				t.Fatalf("RecordArtifacts = %v, want %v", policy.RecordArtifacts, tc.recordArtifacts)
			}
		})
	}
}

func TestContractPolicyEffectiveAppliesModeDefaults(t *testing.T) {
	t.Parallel()

	policy := ContractPolicy{}.Effective(core.ContractValidationStrict)
	if policy.Mode != core.ContractValidationStrict {
		t.Fatalf("Mode = %q, want %q", policy.Mode, core.ContractValidationStrict)
	}
	if !policy.EnforceProjection {
		t.Fatal("expected strict policy to enforce projection")
	}
	if !policy.EnforceWrites {
		t.Fatal("expected strict policy to enforce writes")
	}
	if !policy.RecordArtifacts {
		t.Fatal("expected strict policy to record artifacts")
	}
}

func TestContractPolicyEffectivePreservesExplicitFlags(t *testing.T) {
	t.Parallel()

	policy := ContractPolicy{
		Mode:            core.ContractValidationOff,
		ModeSet:         true,
		RecordArtifacts: true,
	}.Effective(core.ContractValidationStrict)

	if policy.Mode != core.ContractValidationOff {
		t.Fatalf("Mode = %q, want %q", policy.Mode, core.ContractValidationOff)
	}
	if policy.EnforceProjection {
		t.Fatal("did not expect projection to be enabled for explicit off mode")
	}
	if policy.EnforceWrites {
		t.Fatal("did not expect write enforcement to be enabled for explicit off mode")
	}
	if !policy.RecordArtifacts {
		t.Fatal("expected explicit artifact recording to be preserved")
	}
}
