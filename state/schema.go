package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type JSONSchema map[string]any

type compiledJSONSchema struct {
	schema *jsonschema.Schema
	err    error
}

var compiledJSONSchemas sync.Map

func (schema JSONSchema) Clone() JSONSchema {
	if len(schema) == 0 {
		return nil
	}
	cloned := make(JSONSchema, len(schema))
	for key, value := range schema {
		cloned[key] = cloneSchemaValue(value)
	}
	return cloned
}

func (schema JSONSchema) Type() string {
	if len(schema) == 0 {
		return ""
	}
	value, _ := schema["type"].(string)
	return strings.TrimSpace(value)
}

func ValidateJSONSchemaDefinition(schema JSONSchema) error {
	if len(schema) == 0 {
		return nil
	}
	_, err := compileJSONSchema(schema)
	return err
}

func ValidateJSONSchemaValue(value any, schema JSONSchema, basePath string) []ValidationIssue {
	if len(schema) == 0 {
		return nil
	}
	compiled, err := compileJSONSchema(schema)
	if err != nil {
		return []ValidationIssue{{
			Path:    strings.TrimSpace(basePath),
			Kind:    "invalid_json_schema",
			Message: err.Error(),
		}}
	}
	normalized, err := normalizeSchemaValue(value)
	if err != nil {
		return []ValidationIssue{{
			Path:    strings.TrimSpace(basePath),
			Kind:    "schema_value_encoding_failed",
			Message: fmt.Sprintf("encode value for JSON Schema validation: %v", err),
		}}
	}
	if err := compiled.Validate(normalized); err != nil {
		var validationErr *jsonschema.ValidationError
		if !errors.As(err, &validationErr) {
			return []ValidationIssue{{
				Path:    strings.TrimSpace(basePath),
				Kind:    "json_schema_validation_failed",
				Message: err.Error(),
			}}
		}
		issues := schemaValidationIssues(validationErr, strings.TrimSpace(basePath))
		if len(issues) > 0 {
			return issues
		}
	}
	return nil
}

func compileJSONSchema(schema JSONSchema) (*jsonschema.Schema, error) {
	payload, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode JSON Schema: %w", err)
	}
	key := string(payload)
	if cached, ok := compiledJSONSchemas.Load(key); ok {
		result := cached.(compiledJSONSchema)
		return result.schema, result.err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	normalized, normalizeErr := normalizeSchemaValue(schema)
	if normalizeErr != nil {
		result := compiledJSONSchema{err: fmt.Errorf("normalize JSON Schema: %w", normalizeErr)}
		compiledJSONSchemas.Store(key, result)
		return nil, result.err
	}
	if err := compiler.AddResource("weaveflow://state/schema.json", normalized); err != nil {
		result := compiledJSONSchema{err: fmt.Errorf("load JSON Schema: %w", err)}
		compiledJSONSchemas.Store(key, result)
		return nil, result.err
	}
	compiled, err := compiler.Compile("weaveflow://state/schema.json")
	if err != nil {
		err = fmt.Errorf("compile JSON Schema: %w", err)
	}
	result := compiledJSONSchema{schema: compiled, err: err}
	compiledJSONSchemas.Store(key, result)
	return compiled, err
}

func schemaCacheKey(schema JSONSchema) string {
	payload, err := json.Marshal(schema)
	if err != nil {
		return fmt.Sprint(schema)
	}
	return string(payload)
}

func schemaValidationIssues(root *jsonschema.ValidationError, basePath string) []ValidationIssue {
	leaves := make([]*jsonschema.ValidationError, 0)
	collectSchemaValidationLeaves(root, &leaves)
	issues := make([]ValidationIssue, 0, len(leaves))
	seen := map[string]struct{}{}
	for _, leaf := range leaves {
		path := joinSchemaInstancePath(basePath, leaf.InstanceLocation)
		kind := "json_schema_validation_failed"
		if leaf.ErrorKind != nil {
			kind = "json_schema_" + sanitizeSchemaKeyword(fmt.Sprintf("%T", leaf.ErrorKind))
		}
		message := schemaValidationMessage(leaf)
		key := path + "\x00" + kind + "\x00" + message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		issues = append(issues, ValidationIssue{Path: path, Kind: kind, Message: message})
	}
	sort.SliceStable(issues, func(leftIndex, rightIndex int) bool {
		if issues[leftIndex].Path != issues[rightIndex].Path {
			return issues[leftIndex].Path < issues[rightIndex].Path
		}
		if issues[leftIndex].Kind != issues[rightIndex].Kind {
			return issues[leftIndex].Kind < issues[rightIndex].Kind
		}
		return issues[leftIndex].Message < issues[rightIndex].Message
	})
	return issues
}

func collectSchemaValidationLeaves(current *jsonschema.ValidationError, leaves *[]*jsonschema.ValidationError) {
	if current == nil {
		return
	}
	if len(current.Causes) == 0 {
		*leaves = append(*leaves, current)
		return
	}
	for _, cause := range current.Causes {
		collectSchemaValidationLeaves(cause, leaves)
	}
}

func schemaValidationMessage(validationErr *jsonschema.ValidationError) string {
	if validationErr == nil {
		return "value does not satisfy JSON Schema"
	}
	message := strings.TrimSpace(validationErr.Error())
	if lineBreak := strings.LastIndex(message, "\n"); lineBreak >= 0 {
		message = strings.TrimSpace(message[lineBreak+1:])
	}
	if message == "" {
		return "value does not satisfy JSON Schema"
	}
	return message
}

func joinSchemaInstancePath(basePath string, instanceLocation []string) string {
	parts := make([]string, 0, len(instanceLocation)+1)
	if basePath != "" {
		parts = append(parts, basePath)
	}
	for _, segment := range instanceLocation {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	return strings.Join(parts, ".")
}

func sanitizeSchemaKeyword(value string) string {
	value = strings.TrimPrefix(value, "*jsonschema.")
	value = strings.TrimPrefix(value, "jsonschema.")
	value = strings.TrimSuffix(value, "Error")
	var builder strings.Builder
	for index, character := range value {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				builder.WriteByte('_')
			}
			builder.WriteRune(character + ('a' - 'A'))
			continue
		}
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			builder.WriteRune(character)
		}
	}
	if builder.Len() == 0 {
		return "validation_failed"
	}
	return builder.String()
}

func normalizeSchemaValue(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func cloneSchemaValue(value any) any {
	switch typed := value.(type) {
	case JSONSchema:
		return typed.Clone()
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneSchemaValue(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneSchemaValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
