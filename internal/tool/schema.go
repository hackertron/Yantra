package tool

import (
	"encoding/json"
	"fmt"
)

// SchemaType represents a JSON Schema type.
type SchemaType string

const (
	TypeString  SchemaType = "string"
	TypeInteger SchemaType = "integer"
	TypeNumber  SchemaType = "number"
	TypeBoolean SchemaType = "boolean"
	TypeArray   SchemaType = "array"
)

// Prop describes a single property in a JSON Schema object.
type Prop struct {
	Name        string
	Type        SchemaType
	Description string
	Required    bool
	// Items is the schema type for array elements (only used when Type == TypeArray).
	Items *SchemaType
	// Enum constrains the property to a fixed set of values.
	Enum []string
}

// Schema builds a JSON Schema "object" from a list of property descriptors.
// The returned json.RawMessage is suitable for FunctionDecl.Parameters.
func Schema(props ...Prop) json.RawMessage {
	properties := make(map[string]any, len(props))
	var required []string

	for _, p := range props {
		prop := map[string]any{
			"type":        string(p.Type),
			"description": p.Description,
		}
		if p.Type == TypeArray && p.Items != nil {
			prop["items"] = map[string]any{"type": string(*p.Items)}
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	b, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("tool.Schema: marshal failed: %v", err))
	}
	return b
}
