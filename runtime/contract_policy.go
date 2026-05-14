package runtime

import "weaveflow/core"

// ContractPolicy controls which parts of the contract runtime are active.
// Mode-derived defaults are applied first, then explicit true flags extend them.
type ContractPolicy struct {
	Mode              core.ContractValidationMode
	ModeSet           bool
	EnforceProjection bool
	EnforceWrites     bool
	RecordArtifacts   bool
}

func ContractPolicyForMode(mode core.ContractValidationMode) ContractPolicy {
	switch mode {
	case core.ContractValidationStrict:
		return ContractPolicy{
			Mode:              core.ContractValidationStrict,
			ModeSet:           true,
			EnforceProjection: true,
			EnforceWrites:     true,
			RecordArtifacts:   true,
		}
	case core.ContractValidationWarn:
		return ContractPolicy{
			Mode:              core.ContractValidationWarn,
			ModeSet:           true,
			EnforceProjection: true,
			RecordArtifacts:   true,
		}
	default:
		return ContractPolicy{
			Mode:    core.ContractValidationOff,
			ModeSet: true,
		}
	}
}

func (p ContractPolicy) Effective(mode core.ContractValidationMode) ContractPolicy {
	effectiveMode := mode
	if p.ModeSet || p.Mode != "" {
		effectiveMode = p.Mode
	}

	defaults := ContractPolicyForMode(effectiveMode)
	p.Mode = defaults.Mode
	p.EnforceProjection = p.EnforceProjection || defaults.EnforceProjection
	p.EnforceWrites = p.EnforceWrites || defaults.EnforceWrites
	p.RecordArtifacts = p.RecordArtifacts || defaults.RecordArtifacts
	return p
}

func (p ContractPolicy) Enabled() bool {
	return p.Mode != core.ContractValidationOff ||
		p.EnforceProjection ||
		p.EnforceWrites ||
		p.RecordArtifacts
}
