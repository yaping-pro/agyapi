package convert

import (
	"encoding/json"
	"testing"
)

func TestCleanSchemaResolvesRefAndMergesAllOf(t *testing.T) {
	root := map[string]any{
		"$defs": map[string]any{
			"loc": map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
			},
		},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"$ref": "#/$defs/loc", "description": "where"},
		},
		"allOf": []any{
			map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"properties": map[string]any{"b": map[string]any{"type": "integer"}}},
		},
	}

	got := CleanSchema(schema, root)
	if _, ok := got["allOf"]; ok {
		t.Fatalf("allOf should be flattened, got %v", got)
	}
	if _, ok := got["$ref"]; ok {
		t.Fatalf("top-level $ref should not survive, got %v", got)
	}

	props, _ := got["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("properties missing: %v", got)
	}
	target, _ := props["target"].(map[string]any)
	if target == nil || target["$ref"] != nil {
		t.Fatalf("nested $ref not resolved: %v", props["target"])
	}
	if target["description"] != "where" {
		t.Fatalf("sibling description lost during $ref resolution: %v", target)
	}
	sub, _ := target["properties"].(map[string]any)
	if sub == nil || sub["path"] == nil {
		t.Fatalf("resolved $ref body missing: %v", target)
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := props[k]; !ok {
			t.Fatalf("allOf branch %q not merged: %v", k, props)
		}
	}
}

func TestCleanSchemaCollapsesConstUnionToEnum(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"const": "fast"},
			map[string]any{"const": "slow"},
		},
	}
	got := CleanSchema(schema, nil)
	if _, ok := got["anyOf"]; ok {
		t.Fatalf("anyOf not collapsed: %v", got)
	}
	if got["type"] != "string" {
		t.Fatalf("type = %v, want string", got["type"])
	}
	enum, _ := got["enum"].([]string)
	if len(enum) != 2 || enum[0] != "fast" || enum[1] != "slow" {
		t.Fatalf("enum = %v, want [fast slow]", got["enum"])
	}
}

func TestCleanSchemaCoercesNullableUnionType(t *testing.T) {
	schema := map[string]any{"type": []any{"string", "null"}}
	got := CleanSchema(schema, nil)
	if got["type"] != "string" {
		t.Fatalf("type = %v, want string", got["type"])
	}
	desc, _ := got["description"].(string)
	if desc == "" {
		t.Fatalf("nullable hint should move into description, got %v", got)
	}
}

func TestCleanSchemaDropsUnsupportedKeywords(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"properties": map[string]any{
			"x": map[string]any{"type": "string", "format": "uri", "pattern": "^a"},
		},
	}
	got := CleanSchema(schema, nil)
	if _, ok := got["$schema"]; ok {
		t.Fatalf("$schema must be dropped: %v", got)
	}
	props, _ := got["properties"].(map[string]any)
	x, _ := props["x"].(map[string]any)
	if _, ok := x["format"]; ok {
		t.Fatalf("format must be dropped from nested schema: %v", x)
	}
	if _, ok := x["pattern"]; ok {
		t.Fatalf("pattern must be dropped or demoted: %v", x)
	}
	if x["type"] != "string" {
		t.Fatalf("type lost while dropping keywords: %v", x)
	}
}

func TestCleanSchemaHandlesCircularReference(t *testing.T) {
	node := map[string]any{"type": "object"}
	node["properties"] = map[string]any{"self": node}
	got := CleanSchema(node, nil)
	props, _ := got["properties"].(map[string]any)
	self, _ := props["self"].(map[string]any)
	if self == nil {
		t.Fatalf("circular property missing: %v", got)
	}
	if _, err := json.Marshal(self); err != nil {
		t.Fatalf("circular schema not terminated: %v", err)
	}
}

func TestCleanSchemaDoesNotMutateInput(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	before, _ := json.Marshal(schema)
	_ = CleanSchema(schema, nil)
	after, _ := json.Marshal(schema)
	if string(before) != string(after) {
		t.Fatalf("input mutated:\nbefore=%s\nafter =%s", before, after)
	}
}
