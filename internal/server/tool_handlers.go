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
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	Strict      bool   `json:"strict,omitempty"`
}

func (s *Server) handleTools(c *gin.Context) {
	available := s.currentToolSet()

	definitions := make([]toolDefinition, 0, len(available))
	for _, id := range sortedToolKeys(available) {
		tool := available[id]
		name := strings.TrimSpace(id)
		var description string
		var parameters any
		var strict bool
		if tool.Function != nil {
			if functionName := strings.TrimSpace(tool.Function.Name); functionName != "" {
				name = functionName
			}
			description = tool.Function.Description
			parameters = tool.Function.Parameters
			strict = tool.Function.Strict
		}
		definitions = append(definitions, toolDefinition{
			ID:          id,
			Name:        name,
			Description: description,
			Parameters:  parameters,
			Strict:      strict,
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
