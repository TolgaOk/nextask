package integrations

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type Kind int

const (
	String Kind = iota
	Integer
	Boolean
	StringList
)

// Option declares an integration-owned value and its default.
type Option struct {
	Kind    Kind
	Default any
}
type Schema map[string]Option
type Options map[string]any

func (o Options) String(key string) string { v, _ := o[key].(string); return v }

func (s Schema) Resolve(values map[string]any) (Options, error) {
	result := Options{}
	for key, spec := range s {
		value := spec.Default
		if v, exists := values[key]; exists {
			value = v
		}
		normalized, err := spec.normalize(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		result[key] = normalized
	}
	for key := range values {
		if _, ok := s[key]; !ok {
			return nil, fmt.Errorf("unknown option %s", key)
		}
	}
	return result, nil
}

func (s Option) parse(raw string) (any, error) {
	switch s.Kind {
	case String:
		return raw, nil
	case Integer:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected an integer")
		}
		return value, nil
	case Boolean:
		if raw == "true" {
			return true, nil
		}
		if raw == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("expected true or false")
	case StringList:
		var value []string
		if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
			return nil, fmt.Errorf("expected a JSON array of strings")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported option type")
	}
}

func (s Option) normalize(value any) (any, error) {
	switch s.Kind {
	case String:
		if v, ok := value.(string); ok {
			return v, nil
		}
	case Integer:
		switch v := value.(type) {
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case json.Number:
			return v.Int64()
		}
	case Boolean:
		if v, ok := value.(bool); ok {
			return v, nil
		}
	case StringList:
		switch v := value.(type) {
		case []string:
			return append([]string{}, v...), nil
		case []any:
			result := make([]string, len(v))
			for i, item := range v {
				text, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("expected a list of strings")
				}
				result[i] = text
			}
			return result, nil
		}
	default:
		return nil, fmt.Errorf("unsupported option type")
	}
	return nil, fmt.Errorf("invalid value type (expected %s)", []string{"string", "integer", "boolean", "list of strings"}[s.Kind])
}
