package openapi

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

type jsonField struct {
	name   string
	field  reflect.StructField
	tagged bool
}

// jsonFields follows encoding/json promotion and dominance rules, rather than
// Go's field visibility rules, which do not account for JSON names or tags.
func jsonFields(valueType reflect.Type) ([]jsonField, error) {
	fields, err := collectJSONFields(valueType, nil, make(map[reflect.Type]bool))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(fields, compareJSONFields)
	selected := fields[:0]
	for index := 0; index < len(fields); {
		end := index + 1
		for end < len(fields) && fields[end].name == fields[index].name {
			end++
		}
		first := fields[index]
		if end == index+1 || len(first.field.Index) < len(fields[index+1].field.Index) || first.tagged != fields[index+1].tagged {
			selected = append(selected, first)
		}
		index = end
	}
	slices.SortFunc(selected, func(a, b jsonField) int { return slices.Compare(a.field.Index, b.field.Index) })
	return selected, nil
}

func compareJSONFields(a, b jsonField) int {
	if order := strings.Compare(a.name, b.name); order != 0 {
		return order
	}
	if depth := len(a.field.Index) - len(b.field.Index); depth != 0 {
		return depth
	}
	if a.tagged != b.tagged {
		if a.tagged {
			return -1
		}
		return 1
	}
	return 0
}

func collectJSONFields(current reflect.Type, parent []int, visiting map[reflect.Type]bool) ([]jsonField, error) {
	if visiting[current] {
		return nil, nil
	}
	visiting[current] = true
	defer delete(visiting, current)
	var fields []jsonField
	for index := 0; index < current.NumField(); index++ {
		field := current.Field(index)
		field.Index = append(slices.Clone(parent), index)
		candidates, err := collectJSONField(field, visiting)
		if err != nil {
			return nil, err
		}
		fields = append(fields, candidates...)
	}
	return fields, nil
}

func collectJSONField(field reflect.StructField, visiting map[reflect.Type]bool) ([]jsonField, error) {
	underlying := field.Type
	if underlying.Kind() == reflect.Pointer {
		underlying = underlying.Elem()
	}
	if !field.IsExported() && (!field.Anonymous || underlying.Kind() != reflect.Struct) {
		return nil, nil
	}
	tag := field.Tag.Get("json")
	if tag == "-" {
		return nil, nil
	}
	name := tagName(tag)
	if !validJSONFieldName(name) {
		return nil, fmt.Errorf("field %s has a nonportable JSON tag %q; use a valid tag or an explicit body schema", field.Name, tag)
	}
	if field.Anonymous && name == "" && underlying.Kind() == reflect.Struct {
		return collectJSONFields(underlying, field.Index, visiting)
	}
	tagged := name != ""
	if name == "" {
		name = field.Name
	}
	return []jsonField{{name: name, field: field, tagged: tagged}}, nil
}

func validJSONFieldName(name string) bool {
	for _, char := range name {
		if !strings.ContainsRune(" !#$%&()*+-./:;<=>?@[]^_{|}~", char) && !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			return false
		}
	}
	return true
}

func hasMarshaler(valueType, marshalerType reflect.Type) bool {
	return valueType.Implements(marshalerType) || reflect.PointerTo(valueType).Implements(marshalerType)
}

func jsonFieldSchema(field reflect.StructField, visiting map[reflect.Type]bool) (map[string]any, error) {
	_, options, _ := strings.Cut(field.Tag.Get("json"), ",")
	valueType := field.Type
	if valueType.Name() == "" && valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if slices.Contains(strings.Split(options, ","), "string") && !hasMarshaler(field.Type, jsonMarshalerType) {
		switch valueType.Kind() {
		case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
			schema := map[string]any{"type": "string"}
			if field.Type.Kind() == reflect.Pointer {
				return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}, nil
			}
			return schema, nil
		}
	}
	return schemaFor(field.Type, visiting)
}

func schemaFor(valueType reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	if valueType == nil {
		return map[string]any{}, nil
	}
	if valueType.Kind() == reflect.Pointer {
		value, err := schemaFor(valueType.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return map[string]any{"anyOf": []any{value, map[string]any{"type": "null"}}}, nil
	}
	if valueType == timeType {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	if valueType == rawMessageType {
		return map[string]any{}, nil
	}
	if hasMarshaler(valueType, jsonMarshalerType) {
		// Custom marshalers require an explicit schema for their wire format.
		return map[string]any{}, nil
	}
	if hasMarshaler(valueType, textMarshalerType) {
		return map[string]any{"type": "string"}, nil
	}

	if schema := primitiveSchema(valueType.Kind()); schema != nil {
		return schema, nil
	}
	return complexSchema(valueType, visiting)
}

func primitiveSchema(kind reflect.Kind) map[string]any {
	switch kind {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int:
		return map[string]any{"type": "integer"}
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return map[string]any{"type": "integer", "format": "int32"}
	case reflect.Int64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	case reflect.String:
		return map[string]any{"type": "string"}
	}
	return nil
}

func complexSchema(valueType reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	switch valueType.Kind() {
	case reflect.Slice:
		if valueType.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "array", "items": items}, err
	case reflect.Array:
		items, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "array", "items": items, "minItems": valueType.Len(), "maxItems": valueType.Len()}, err
	case reflect.Map:
		value, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "object", "additionalProperties": value}, err
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.Struct:
		return structSchema(valueType, visiting)

	default:
		return nil, fmt.Errorf("unsupported Go type %s", valueType)
	}
}

func structSchema(valueType reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	if visiting[valueType] {
		// ponytail: inline schemas collapse recursive edges. Add component
		// references if exact recursive schemas become necessary.
		return map[string]any{"type": "object"}, nil
	}
	visiting[valueType] = true
	defer delete(visiting, valueType)
	properties := make(map[string]any)
	required := make([]string, 0)
	fields, err := jsonFields(valueType)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		property, err := jsonFieldSchema(field.field, visiting)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.name, err)
		}
		properties[field.name] = property
		if requiredField(field.field) {
			required = append(required, field.name)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}
