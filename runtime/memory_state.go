package runtime

const StateKeyMemory = "memory"

func Memory(state State) State {
	return memoryState(state, false)
}

func EnsureMemory(state State) State {
	return memoryState(state, true)
}

func memoryState(state State, create bool) State {
	return rootObjectState(state, StateKeyMemory, create)
}
