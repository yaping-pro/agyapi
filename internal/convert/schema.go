package convert

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// maxSchemaDepth is a hard backstop against pathological nesting that the
// pointer-based cycle detector cannot see (e.g. freshly allocated nodes).
const maxSchemaDepth = 32

// allowedSchemaKeys is the Gemini parametersJsonSchema subset. Everything else
// (format, pattern, $schema, additionalProperties, ...) is either demoted into
// the description or dropped, because the backend rejects unknown keywords.
var allowedSchemaKeys = map[string]struct{}{
	"type":        {},
	"description": {},
	"properties":  {},
	"items":       {},
	"required":    {},
	"enum":        {},
	"nullable":    {},
	"minimum":     {},
	"maximum":     {},
}

// CleanSchema converts an arbitrary JSON Schema into the dialect accepted by
// Code Assist's parametersJsonSchema field. The input is never mutated.
//
// Handling:
//   - $ref / $defs are resolved in place (sibling description kept)
//   - allOf is flattened into a single object schema
//   - anyOf/oneOf of consts collapses to an enum
//   - ["string","null"] becomes "string" plus a description hint
//   - unsupported keywords are dropped; fragile constraints move to description
//   - cycles terminate with an object placeholder
func CleanSchema(schema map[string]any, root map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
	}
	if root == nil {
		root = schema
	}
	return cleanSchema(schema, root, map[uintptr]bool{}, 0)
}

func cleanSchema(in map[string]any, root map[string]any, seen map[uintptr]bool, depth int) map[string]any {
	if in == nil {
		return map[string]any{"type": "object"}
	}
	if depth > maxSchemaDepth {
		return map[string]any{"type": "object", "description": "schema nesting too deep"}
	}

	ptr := reflect.ValueOf(in).Pointer()
	if ptr != 0 {
		if seen[ptr] {
			return map[string]any{"type": "object", "description": "circular reference"}
		}
		seen[ptr] = true
		defer delete(seen, ptr)
	}

	result := make(map[string]any, len(in))
	for k, v := range in {
		result[k] = v
	}

	// 1. Resolve $ref against the document root.
	if refKey, ok := refKeyOf(result); ok {
		if target := resolveRef(refKey, root); target != nil {
			base := cleanSchema(target, root, seen, depth+1)
			if desc, ok := result["description"]; ok {
				base["description"] = desc
			}
			result = base
		} else {
			delete(result, refKey)
		}
	}

	// 2. Flatten allOf.
	if raw, ok := result["allOf"].([]any); ok {
		merged := map[string]any{}
		props := map[string]any{}
		var required []string
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			cleaned := cleanSchema(m, root, seen, depth+1)
			if p, ok := cleaned["properties"].(map[string]any); ok {
				for k, v := range p {
					props[k] = v
				}
				delete(cleaned, "properties")
			}
			if r, ok := cleaned["required"].([]string); ok {
				required = append(required, r...)
				delete(cleaned, "required")
			}
			for k, v := range cleaned {
				if _, exists := merged[k]; !exists {
					merged[k] = v
				}
			}
		}
		if len(props) > 0 {
			merged["properties"] = props
		}
		if len(required) > 0 {
			merged["required"] = dedupeStrings(required)
		}
		delete(result, "allOf")
		for k, v := range merged {
			if k == "properties" || k == "required" {
				continue
			}
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
		// Merge properties/required with whatever the outer schema already had.
		if mergedProps, ok := merged["properties"].(map[string]any); ok {
			if outer, ok := result["properties"].(map[string]any); ok {
				for k, v := range outer {
					if _, exists := mergedProps[k]; !exists {
						mergedProps[k] = v
					}
				}
			}
			result["properties"] = mergedProps
		}
		if mergedReq, ok := merged["required"].([]string); ok {
			if outer, ok := result["required"].([]string); ok {
				mergedReq = append(mergedReq, outer...)
			}
			result["required"] = dedupeStrings(mergedReq)
		}
	}

	// 3. Collapse anyOf / oneOf.
	for _, key := range []string{"anyOf", "oneOf"} {
		raw, ok := result[key].([]any)
		if !ok {
			continue
		}
		if collapsed := collapseUnion(raw); collapsed != nil {
			desc, hasDesc := result["description"]
			if hasDesc {
				collapsed["description"] = desc
			}
			result = collapsed
		} else {
			// Fall back to the most structured branch and keep the original
			// description as a hint.
			if preferred := pickPreferredBranch(raw, root, seen, depth); preferred != nil {
				desc, hasDesc := result["description"]
				if hasDesc {
					appendHint(preferred, fmt.Sprint(desc))
				}
				result = preferred
			}
		}
		delete(result, key)
	}

	// 4. Normalize "type".
	if t, ok := result["type"]; ok {
		result["type"], ok = normalizeType(t)
		if !ok {
			delete(result, "type")
		} else if isNullableUnion(t) {
			appendHint(result, "nullable")
		}
	}

	// 5. Recurse into properties.
	if props, ok := result["properties"].(map[string]any); ok {
		cleaned := make(map[string]any, len(props))
		for name, child := range props {
			if childMap, ok := child.(map[string]any); ok {
				cleaned[name] = cleanSchema(childMap, root, seen, depth+1)
			} else {
				cleaned[name] = child
			}
		}
		result["properties"] = cleaned
	}

	// 6. Recurse into items; tuples degrade to the first member.
	if items, ok := result["items"]; ok {
		switch v := items.(type) {
		case map[string]any:
			result["items"] = cleanSchema(v, root, seen, depth+1)
		case []any:
			if len(v) > 0 {
				if first, ok := v[0].(map[string]any); ok {
					result["items"] = cleanSchema(first, root, seen, depth+1)
				} else {
					delete(result, "items")
				}
				appendHint(result, "tuple schema simplified")
			} else {
				delete(result, "items")
			}
		}
	}

	// 7. Promote unsupported constraint keywords into the description, then
	// drop every key outside the accepted subset.
	demoteKeywords(result)

	return result
}

func refKeyOf(schema map[string]any) (string, bool) {
	if v, ok := schema["$ref"].(string); ok && v != "" {
		return v, true
	}
	if v, ok := schema["ref"].(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// resolveRef follows a local JSON pointer such as "#/$defs/loc".
func resolveRef(ref string, root map[string]any) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	node := any(root)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node, ok = m[seg]
		if !ok {
			return nil
		}
	}
	target, _ := node.(map[string]any)
	return target
}

// collapseUnion converts a union of literal consts into an enum.
func collapseUnion(branches []any) map[string]any {
	if len(branches) == 0 {
		return nil
	}
	var values []any
	for _, b := range branches {
		m, ok := b.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m["const"]
		if !ok || v == nil || v == "" {
			return nil
		}
		values = append(values, v)
	}

	allStrings := true
	for _, v := range values {
		if _, ok := v.(string); !ok {
			allStrings = false
			break
		}
	}
	out := map[string]any{}
	if allStrings {
		enum := make([]string, 0, len(values))
		for _, v := range values {
			enum = append(enum, v.(string))
		}
		out["type"] = "string"
		out["enum"] = enum
		return out
	}

	// Non-string literals: keep the enum array, infer a coarse type.
	out["enum"] = values
	switch values[0].(type) {
	case float64, int, int64:
		out["type"] = "number"
	case bool:
		out["type"] = "boolean"
	}
	return out
}

func pickPreferredBranch(branches []any, root map[string]any, seen map[uintptr]bool, depth int) map[string]any {
	var best map[string]any
	bestScore := -1
	for _, b := range branches {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		cleaned := cleanSchema(m, root, seen, depth+1)
		score := 0
		if _, ok := cleaned["properties"]; ok {
			score += 2
		}
		if cleaned["type"] == "object" || cleaned["type"] == "array" {
			score += 2
		}
		if _, ok := cleaned["enum"]; ok {
			score++
		}
		if _, ok := cleaned["type"]; ok {
			score++
		}
		if score > bestScore {
			best, bestScore = cleaned, score
		}
	}
	return best
}

func normalizeType(t any) (any, bool) {
	switch v := t.(type) {
	case string:
		lower := strings.ToLower(v)
		switch lower {
		case "string", "number", "integer", "boolean", "array", "object":
			return lower, true
		case "null":
			return "string", true
		default:
			return nil, false
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.ToLower(s) != "null" {
				lower := strings.ToLower(s)
				switch lower {
				case "string", "number", "integer", "boolean", "array", "object":
					return lower, true
				}
			}
		}
		return "string", true
	case []string:
		for _, s := range v {
			lower := strings.ToLower(s)
			if lower != "null" {
				switch lower {
				case "string", "number", "integer", "boolean", "array", "object":
					return lower, true
				}
			}
		}
		return "string", true
	}
	return nil, false
}

func isNullableUnion(t any) bool {
	switch v := t.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.ToLower(s) == "null" {
				return true
			}
		}
	case []string:
		for _, s := range v {
			if strings.ToLower(s) == "null" {
				return true
			}
		}
	}
	return false
}

// demoteKeywords moves informational keywords into the description and strips
// everything the backend does not understand.
func demoteKeywords(schema map[string]any) {
	var hints []string
	for _, key := range []string{"format", "pattern", "const", "examples", "title"} {
		if v, ok := schema[key]; ok && v != nil && v != "" {
			hints = append(hints, fmt.Sprintf("%s=%v", key, v))
		}
	}
	if ap, ok := schema["additionalProperties"]; ok {
		if b, isBool := ap.(bool); isBool && !b {
			hints = append(hints, "additionalProperties=false")
		}
	}

	for key := range schema {
		if _, ok := allowedSchemaKeys[key]; !ok {
			delete(schema, key)
		}
	}
	if len(hints) > 0 {
		desc, _ := schema["description"].(string)
		schema["description"] = strings.TrimSpace(desc + " (" + strings.Join(hints, "; ") + ")")
	}
}

func appendHint(schema map[string]any, hint string) {
	if hint == "" {
		return
	}
	if desc, ok := schema["description"].(string); ok && desc != "" {
		schema["description"] = desc + " (" + hint + ")"
		return
	}
	schema["description"] = hint
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
