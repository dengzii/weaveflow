package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"weaveflow/builtin"
)

func main() {
	outputDir := ".local/dsl"
	if len(os.Args) > 1 && os.Args[1] != "" {
		outputDir = os.Args[1]
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		panic(err)
	}

	registry := builtin.NewDefaultRegistry()

	registryPath := filepath.Join(outputDir, "default_registry.json")
	schemaPath := filepath.Join(outputDir, "graph_definition.schema.json")

	writeJSON(registryPath, registry)
	writeJSON(schemaPath, registry.JSONSchema())

	fmt.Printf("wrote default registry to %s\n", registryPath)
	fmt.Printf("wrote graph definition schema to %s\n", schemaPath)
}

func writeJSON(path string, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}
