package nodes

import (
	"weaveflow/core"
	wfstate "weaveflow/state"
)

type Node = core.Node[wfstate.State, wfstate.StatePatch]
type NodeInfo = core.NodeInfo
