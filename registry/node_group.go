package registry

type NodeGroup struct {
	Name      string   `json:"name"`
	NodeTypes []string `json:"node_types"`
}
