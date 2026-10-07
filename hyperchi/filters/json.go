package filters

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strconv"
	"strings"
)

func registerJSONFilters() {
	Register("toJSON", toJSON)
	Register("fromJSON", fromJSON)
	Register("prettyJSON", prettyJSON)
	Register("compactJSON", compactJSON)
	Register("jsonEscape", jsonEscape)
	Register("jsonAttr", jsonAttr)
	Register("jsonLD", jsonLD)
	Register("jsonGet", jsonGet)
	Register("jsonSet", jsonSet)
	Register("jsonMerge", jsonMerge)
	Register("jsonArray", jsonArray)
	Register("jsonObject", jsonObject)
}

func toJSON(v any) (string, error) {
	if v == nil {
		return "null", nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return string(data), nil
}

func fromJSON(s string) (any, error) {
	if s == "" {
		return nil, fmt.Errorf("empty JSON string")
	}

	var result any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return result, nil
}

func prettyJSON(v any) (string, error) {
	if v == nil {
		return "null", nil
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal pretty JSON: %w", err)
	}
	return string(data), nil
}

func compactJSON(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty JSON string")
	}

	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "", fmt.Errorf("failed to unmarshal JSON for compacting: %w", err)
	}

	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compact JSON: %w", err)
	}
	return string(data), nil
}

func jsonEscape(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Drop the quotes json.Marshal puts around a string.
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		return string(data[1 : len(data)-1])
	}
	return string(data)
}

// jsonAttr writes name='<JSON>' with the JSON HTML-escaped. A name that is
// not a safe attribute name, or a value json.Marshal rejects, returns an
// error.
func jsonAttr(name string, value any) (template.HTMLAttr, error) {
	if err := checkAttrName("jsonAttr", name, name); err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("jsonAttr: %w", err)
	}
	return template.HTMLAttr(fmt.Sprintf(`%s='%s'`, name, template.HTMLEscapeString(string(data)))), nil
}

func jsonLD(data any) template.HTML {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return ""
	}

	return template.HTML(fmt.Sprintf(`<script type="application/ld+json">
%s
</script>`, jsonData))
}

// jsonGet follows a dot-separated path such as "user.name" or "items.0"
// through maps and []any, returning nil when a step is missing or an index is
// negative or out of range.
func jsonGet(data any, path string) any {
	current := data
	parts := strings.Split(path, ".")

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil
			}
		case map[string]string:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil
			}
		case map[string]int:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil
			}
		case []any:
			if idx, err := strconv.Atoi(part); err == nil && idx >= 0 && idx < len(v) {
				current = v[idx]
			} else {
				return nil
			}
		default:
			return nil
		}
	}

	return current
}

// jsonSet returns a deep copy of data with value stored at the dot-separated
// path, creating intermediate maps. When a step on the path is not a map it
// returns data unchanged.
func jsonSet(data any, path string, value any) any {
	// Copy first so the template's data is never modified.
	dataCopy := deepCopy(data)

	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return data
	}

	current := dataCopy
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]

		switch v := current.(type) {
		case map[string]any:
			if _, ok := v[part]; !ok {
				v[part] = make(map[string]any)
			}
			current = v[part]
		default:
			return data // Can't set on non-map
		}
	}

	lastPart := parts[len(parts)-1]
	if m, ok := current.(map[string]any); ok {
		m[lastPart] = value
	}

	return dataCopy
}

// jsonMerge returns overlay unless both arguments are maps. Two maps merge
// key by key into a new map, recursing into keys both define, with overlay
// winning.
func jsonMerge(base, overlay any) any {
	baseMap, baseOk := base.(map[string]any)
	overlayMap, overlayOk := overlay.(map[string]any)

	if !baseOk || !overlayOk {
		return overlay // If not both maps, overlay wins
	}

	result := make(map[string]any)

	for k, v := range baseMap {
		result[k] = v
	}

	for k, v := range overlayMap {
		if existingVal, exists := result[k]; exists {
			result[k] = jsonMerge(existingVal, v)
		} else {
			result[k] = v
		}
	}

	return result
}

func jsonArray(items ...any) []any {
	return items
}

func jsonObject(pairs ...any) map[string]any {
	result := make(map[string]any)

	for i := 0; i < len(pairs)-1; i += 2 {
		key, ok := pairs[i].(string)
		if ok {
			result[key] = pairs[i+1]
		}
	}

	return result
}

func deepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			result[k] = deepCopy(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = deepCopy(v)
		}
		return result
	default:
		return v
	}
}
