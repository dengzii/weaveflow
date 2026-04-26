package runtime

const (
	StateKeyToolPolicyCheck = "tool_policy_check"
	StateKeyApproval        = "approval"
	StateKeyBudget          = "budget"
)

func ToolPolicyCheck(state State) State {
	return rootObjectState(state, StateKeyToolPolicyCheck, false)
}

func EnsureToolPolicyCheck(state State) State {
	return rootObjectState(state, StateKeyToolPolicyCheck, true)
}

func Approval(state State) State {
	return rootObjectState(state, StateKeyApproval, false)
}

func EnsureApproval(state State) State {
	return rootObjectState(state, StateKeyApproval, true)
}

func Budget(state State) State {
	return rootObjectState(state, StateKeyBudget, false)
}

func EnsureBudget(state State) State {
	return rootObjectState(state, StateKeyBudget, true)
}
