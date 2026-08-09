// Package tools provides bundled tool implementations for agent workflows.
package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dengzii/weaveflow/core"
)

type Tool = core.Tool
type ToolHandler = core.ToolHandler

func decodeToolRequest(input string, toolName string, target any) error {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return fmt.Errorf("%s input is required", toolName)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s input must be valid JSON: %w", toolName, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s input contains multiple JSON values", toolName)
		}
		return fmt.Errorf("%s input must contain one JSON value: %w", toolName, err)
	}
	return nil
}
