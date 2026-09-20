package openapi

import (
	"fmt"
	"reflect"
)

func describeParameters(requestType reflect.Type, parameters []parameterDocument, indexes map[string]int, pathParameters map[string]bool) ([]parameterDocument, error) {
	if requestType != nil && requestType.Kind() == reflect.Struct {
		for index := 0; index < requestType.NumField(); index++ {
			field := requestType.Field(index)
			if !field.IsExported() {
				continue
			}
			for _, source := range []string{"path", "query", "header", "cookie"} {
				name := tagName(field.Tag.Get(source))
				if name == "" || name == "-" {
					continue
				}
				parameter, err := describeParameter(field, name, source, pathParameters)
				if err != nil {
					return nil, err
				}
				key := parameterKey(source, name)
				if existing, ok := indexes[key]; ok {
					parameters[existing] = parameter
					continue
				}
				indexes[key] = len(parameters)
				parameters = append(parameters, parameter)
			}
		}
	}

	return parameters, nil
}

func describeParameter(field reflect.StructField, name, source string, pathParameters map[string]bool) (parameterDocument, error) {
	if source == "path" && !pathParameters[name] {
		return parameterDocument{}, fmt.Errorf("request path field %q is not present in the route", name)
	}
	schema, err := schemaFor(field.Type, make(map[reflect.Type]bool))
	if err != nil {
		return parameterDocument{}, fmt.Errorf("request field %s: %w", field.Name, err)
	}
	return parameterDocument{Name: name, In: source, Schema: schema, Required: source == "path" || requiredField(field)}, nil
}
