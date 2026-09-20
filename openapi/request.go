package openapi

import (
	"fmt"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	vial "github.com/jrgf/go-vial"
)

func describeRequest(route vial.Route, operation Operation) ([]parameterDocument, *requestBodyDocument, error) {
	parameters := make([]parameterDocument, 0, len(route.Parameters))
	indexes := make(map[string]int)
	pathParameters := make(map[string]bool, len(route.Parameters))
	for _, name := range route.Parameters {
		pathParameters[name] = true
		indexes["path\x00"+name] = len(parameters)
		parameters = append(parameters, parameterDocument{Name: name, In: "path", Required: true, Schema: map[string]any{"type": "string"}})
	}
	if operation.Request == nil && len(operation.RequestSchema) == 0 {
		return parameters, nil, nil
	}

	requestType := reflect.TypeOf(operation.Request)
	for requestType != nil && requestType.Kind() == reflect.Pointer {
		requestType = requestType.Elem()
	}
	parameters, err := describeParameters(requestType, parameters, indexes, pathParameters)
	if err != nil {
		return nil, nil, err
	}

	requestBody, err := describeRequestBody(requestType, operation)
	return parameters, requestBody, err
}

func requestSchema(requestType reflect.Type, contentType string) (map[string]any, bool, error) {
	if requestType.Kind() != reflect.Struct {
		schema, err := schemaFor(requestType, make(map[reflect.Type]bool))
		return schema, true, err
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, false, fmt.Errorf("invalid request content type %q", contentType)
	}
	form := mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data"
	if !form && (hasMarshaler(requestType, jsonMarshalerType) || hasMarshaler(requestType, textMarshalerType)) {
		schema, err := schemaFor(requestType, make(map[reflect.Type]bool))
		return schema, true, err
	}
	properties, required, err := requestProperties(requestType, form)
	if err != nil || len(properties) == 0 {
		return nil, false, err
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, true, nil
}

func requestProperties(requestType reflect.Type, form bool) (map[string]any, []string, error) {
	fields, err := requestFields(requestType, form)
	if err != nil {
		return nil, nil, err
	}
	properties, required := make(map[string]any), make([]string, 0)
	for _, field := range fields {
		var schema map[string]any
		if form {
			schema, err = schemaFor(field.field.Type, make(map[reflect.Type]bool))
		} else {
			schema, err = jsonFieldSchema(field.field, make(map[reflect.Type]bool))
		}
		if err != nil {
			return nil, nil, fmt.Errorf("request field %s: %w", field.name, err)
		}
		properties[field.name] = schema
		if requiredField(field.field) {
			required = append(required, field.name)
		}
	}
	return properties, required, nil
}

func requestFields(requestType reflect.Type, form bool) ([]jsonField, error) {
	if !form {
		fields, err := jsonFields(requestType)
		if err != nil {
			return nil, err
		}
		return slices.DeleteFunc(fields, func(field jsonField) bool {
			_, include := requestFieldName(requestType.Field(field.field.Index[0]), false)
			return !include
		}), nil
	}
	var fields []jsonField
	for index := 0; index < requestType.NumField(); index++ {
		field := requestType.Field(index)
		name, include := requestFieldName(field, true)
		if field.IsExported() && include {
			fields = append(fields, jsonField{name: name, field: field})
		}
	}
	return fields, nil
}

func requestFieldName(field reflect.StructField, form bool) (string, bool) {
	if form {
		name := tagName(field.Tag.Get("form"))
		return name, name != "" && name != "-"
	}
	jsonTag, hasJSONTag := field.Tag.Lookup("json")
	name := tagName(jsonTag)
	if jsonTag == "-" {
		return "", false
	}
	explicit := false
	for _, source := range []string{"path", "query", "header", "cookie", "form"} {
		candidate := tagName(field.Tag.Get(source))
		if candidate != "" && candidate != "-" {
			explicit = true
			break
		}
	}
	if explicit && !hasJSONTag {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	return name, true
}

func describeResponses(configured map[int]Response) (map[string]responseDocument, error) {
	if len(configured) == 0 {
		return map[string]responseDocument{"200": {Description: http.StatusText(http.StatusOK)}}, nil
	}
	responses := make(map[string]responseDocument, len(configured))
	for status, configuredResponse := range configured {
		if status < 100 || status > 599 {
			return nil, fmt.Errorf("invalid response status %d", status)
		}
		response, err := describeResponse(status, configuredResponse)
		if err != nil {
			return nil, err
		}
		responses[strconv.Itoa(status)] = response
	}
	return responses, nil
}

func describeResponse(status int, configured Response) (responseDocument, error) {
	response := responseDocument{Description: configured.Description}
	if response.Description == "" {
		response.Description = http.StatusText(status)
	}
	if response.Description == "" {
		response.Description = "Response"
	}
	if configured.Body == nil && configured.ContentType == "" && len(configured.Schema) == 0 {
		return response, nil
	}
	schema, err := responseSchema(configured)
	if err != nil {
		return responseDocument{}, fmt.Errorf("response %d: %w", status, err)
	}
	contentType := configured.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return responseDocument{}, fmt.Errorf("invalid response content type %q", contentType)
	}
	response.Content = map[string]mediaDocument{contentType: {Schema: schema}}
	return response, nil
}

func tagName(tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func requiredField(field reflect.StructField) bool {
	for option := range strings.SplitSeq(field.Tag.Get("openapi"), ",") {
		if strings.TrimSpace(option) == "required" {
			return true
		}
	}
	return false
}

func parameterKey(source, name string) string {
	if source == "header" {
		name = strings.ToLower(name)
	}
	return source + "\x00" + name
}

func describeRequestBody(requestType reflect.Type, operation Operation) (*requestBodyDocument, error) {
	contentType := operation.RequestContentType
	if contentType == "" {
		contentType = "application/json"
	}
	var bodySchema any = operation.RequestSchema
	if len(operation.RequestSchema) == 0 {
		inferred, present, err := requestSchema(requestType, contentType)
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, nil
		}
		bodySchema = inferred
	}
	requestBody := &requestBodyDocument{Required: operation.RequestRequired, Content: map[string]mediaDocument{contentType: {Schema: bodySchema}}}
	return requestBody, nil
}

func responseSchema(configured Response) (any, error) {
	if len(configured.Schema) > 0 {
		return configured.Schema, nil
	}
	if configured.Body == nil {
		return map[string]any{}, nil
	}
	return schemaFor(reflect.TypeOf(configured.Body), make(map[reflect.Type]bool))
}
