package tools

// BuiltinFactories returns the bundled workspace-tool constructors keyed by
// their function names. A fresh map is returned for each call.
func BuiltinFactories() map[string]func() Tool {
	return map[string]func() Tool{
		"read":    NewRead,
		"outline": NewOutline,
		"write":   NewWrite,
		"edit":    NewEdit,
		"grep":    NewGrep,
		"glob":    NewGlob,
	}
}
