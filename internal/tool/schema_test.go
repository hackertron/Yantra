package tool

import (
	"encoding/json"
	"testing"
)

func TestSchema_RoundTrip(t *testing.T) {
	s := Schema(
		Prop{Name: "path", Type: TypeString, Description: "File path", Required: true},
		Prop{Name: "limit", Type: TypeInteger, Description: "Max lines", Required: false},
	)

	var parsed map[string]any
	if err := json.Unmarshal(s, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["type"] != "object" {
		t.Errorf("expected type=object, got %v", parsed["type"])
	}

	props, ok := parsed["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not a map")
	}

	pathProp, ok := props["path"].(map[string]any)
	if !ok {
		t.Fatal("path property missing")
	}
	if pathProp["type"] != "string" {
		t.Errorf("expected path type=string, got %v", pathProp["type"])
	}

	limitProp, ok := props["limit"].(map[string]any)
	if !ok {
		t.Fatal("limit property missing")
	}
	if limitProp["type"] != "integer" {
		t.Errorf("expected limit type=integer, got %v", limitProp["type"])
	}
}

func TestSchema_RequiredFields(t *testing.T) {
	s := Schema(
		Prop{Name: "a", Type: TypeString, Description: "required field", Required: true},
		Prop{Name: "b", Type: TypeString, Description: "optional field", Required: false},
		Prop{Name: "c", Type: TypeBoolean, Description: "also required", Required: true},
	)

	var parsed map[string]any
	if err := json.Unmarshal(s, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	required, ok := parsed["required"].([]any)
	if !ok {
		t.Fatal("required not an array")
	}

	reqSet := make(map[string]bool)
	for _, r := range required {
		reqSet[r.(string)] = true
	}

	if !reqSet["a"] {
		t.Error("expected 'a' in required")
	}
	if reqSet["b"] {
		t.Error("'b' should not be in required")
	}
	if !reqSet["c"] {
		t.Error("expected 'c' in required")
	}
}

func TestSchema_ArrayType(t *testing.T) {
	itemType := TypeString
	s := Schema(
		Prop{Name: "tags", Type: TypeArray, Description: "tag list", Required: false, Items: &itemType},
	)

	var parsed map[string]any
	if err := json.Unmarshal(s, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	props := parsed["properties"].(map[string]any)
	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Errorf("expected type=array, got %v", tags["type"])
	}
	items, ok := tags["items"].(map[string]any)
	if !ok {
		t.Fatal("items not present for array type")
	}
	if items["type"] != "string" {
		t.Errorf("expected items.type=string, got %v", items["type"])
	}
}

func TestSchema_NoRequired(t *testing.T) {
	s := Schema(
		Prop{Name: "x", Type: TypeString, Description: "optional", Required: false},
	)

	var parsed map[string]any
	if err := json.Unmarshal(s, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if _, ok := parsed["required"]; ok {
		t.Error("expected no required field when all props are optional")
	}
}
