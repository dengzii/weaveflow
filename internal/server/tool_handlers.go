package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/dengzii/weaveflow/core"

	"github.com/gin-gonic/gin"
)

type toolsResponse struct {
	Tools []toolDefinition `json:"tools"`
}

type toolDefinition struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Description  string                `json:"description,omitempty"`
	Parameters   any                   `json:"parameters,omitempty"`
	OutputSchema any                   `json:"output_schema,omitempty"`
	Strict       bool                  `json:"strict,omitempty"`
	Permissions  []string              `json:"permissions"`
	Approval     core.ToolApprovalMode `json:"approval"`
}

func (s *Server) handleListRuntimeTools(c *gin.Context) {
	available := s.currentToolSet()

	definitions := make([]toolDefinition, 0, len(available))
	for _, id := range sortedToolKeys(available) {
		tool := available[id]
		name := strings.TrimSpace(id)
		var description string
		var parameters any
		var outputSchema any
		var strict bool
		if tool.Function != nil {
			if functionName := strings.TrimSpace(tool.Function.Name); functionName != "" {
				name = functionName
			}
			description = tool.Function.Description
			parameters = tool.Function.Parameters
			outputSchema = tool.Function.OutputSchema
			strict = tool.Function.Strict
		}
		definitions = append(definitions, toolDefinition{
			ID:           id,
			Name:         name,
			Description:  description,
			Parameters:   parameters,
			OutputSchema: outputSchema,
			Strict:       strict,
			Permissions:  append([]string(nil), tool.Permissions...),
			Approval:     tool.Approval,
		})
	}

	writeData(c, http.StatusOK, toolsResponse{Tools: definitions})
}

func sortedToolKeys(input map[string]core.Tool) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
