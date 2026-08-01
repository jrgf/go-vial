package vial

import (
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/jrgf/go-vial/fault"
)

const defaultMultipartMemory = int64(1 << 20) // 1 MiB

var bindingMetadataCache sync.Map

var bindingSources = [...]string{"path", "query", "header", "cookie", "form"}

// BindingError identifies the request field that could not be bound.
type BindingError struct {
	Source string
	Field  string
	Cause  error
}

func (err *BindingError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s field %q: %v", err.Source, err.Field, err.Cause)
}

func (err *BindingError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// Bind combines tagged path, query, header, cookie, JSON, and form input.
func (context *Context) Bind(destination any) error {
	for _, bind := range []func(any) error{
		context.BindPath,
		context.BindQuery,
		context.BindHeader,
		context.BindCookie,
	} {
		if err := bind(destination); err != nil {
			return err
		}
	}

	contentType := context.request.Header.Get("Content-Type")
	if contentType == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return UnsupportedMediaType("unsupported_media_type", "Content-Type is invalid")
	}
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return context.BindJSON(destination)
	case mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data":
		return context.BindForm(destination)
	default:
		return UnsupportedMediaType("unsupported_media_type", "Content-Type is not supported for binding")
	}
}

// BindPath binds route parameters to fields tagged with `path`.
func (context *Context) BindPath(destination any) error {
	return bindRequestValues(destination, "path", "invalid_path", "Path parameters contain invalid values", func(name string) ([]string, bool) {
		value := context.request.PathValue(name)
		return []string{value}, value != ""
	})
}

// BindQuery binds query parameters to primitive struct fields tagged with
// `query`. Repeated values bind to slices.
func (context *Context) BindQuery(destination any) error {
	return bindValues(destination, "query", "invalid_query", "Query parameters contain invalid values", context.request.URL.Query())
}

// BindHeader binds request headers to fields tagged with `header`.
func (context *Context) BindHeader(destination any) error {
	return bindRequestValues(destination, "header", "invalid_header", "Request headers contain invalid values", func(name string) ([]string, bool) {
		values := context.request.Header.Values(name)
		return values, len(values) > 0
	})
}

// BindCookie binds request cookies to fields tagged with `cookie`.
func (context *Context) BindCookie(destination any) error {
	return bindRequestValues(destination, "cookie", "invalid_cookie", "Request cookies contain invalid values", func(name string) ([]string, bool) {
		values := make([]string, 0, 1)
		for _, cookie := range context.request.Cookies() {
			if cookie.Name == name {
				values = append(values, cookie.Value)
			}
		}
		return values, len(values) > 0
	})
}

// BindForm binds URL-encoded or multipart form fields to primitive struct
// fields tagged with `form`. Repeated values bind to slices.
func (context *Context) BindForm(destination any) error {
	values, err := context.parseForm()
	if err != nil {
		return err
	}
	return bindValues(destination, "form", "invalid_form", "Form contains invalid values", values)
}

// FormFile returns the first uploaded file for name. The caller must close the
// file. Vial removes any temporary multipart files after the request finishes.
func (context *Context) FormFile(name string) (multipart.File, *multipart.FileHeader, error) {
	if err := context.parseMultipartForm(); err != nil {
		return nil, nil, err
	}
	file, header, err := context.request.FormFile(name)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil, BadRequest("missing_file", fmt.Sprintf("Form file %q is required", name))
	}
	if err != nil {
		return nil, nil, WrapHTTPError(
			http.StatusBadRequest,
			"invalid_multipart",
			"Multipart form is invalid",
			err,
		)
	}
	return file, header, nil
}

func (context *Context) parseForm() (url.Values, error) {
	mediaType, err := requestMediaType(context.request)
	if err != nil || (mediaType != "application/x-www-form-urlencoded" && mediaType != "multipart/form-data") {
		return nil, UnsupportedMediaType(
			"unsupported_media_type",
			"Content-Type must be application/x-www-form-urlencoded or multipart/form-data",
		)
	}

	context.limitBody()
	if mediaType == "multipart/form-data" {
		err = context.request.ParseMultipartForm(defaultMultipartMemory)
	} else {
		err = context.request.ParseForm()
	}
	if err != nil {
		return nil, formParseError(err, "invalid_form", "Form contains invalid data")
	}
	return context.request.PostForm, nil
}

func (context *Context) parseMultipartForm() error {
	mediaType, err := requestMediaType(context.request)
	if err != nil || mediaType != "multipart/form-data" {
		return UnsupportedMediaType(
			"unsupported_media_type",
			"Content-Type must be multipart/form-data",
		)
	}

	context.limitBody()
	if err := context.request.ParseMultipartForm(defaultMultipartMemory); err != nil {
		return formParseError(err, "invalid_multipart", "Multipart form is invalid")
	}
	return nil
}

func requestMediaType(request *http.Request) (string, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return mediaType, err
}

func formParseError(err error, code, message string) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return RequestEntityTooLarge(
			"request_body_too_large",
			"Request body exceeds the configured limit",
		)
	}
	return WrapHTTPError(
		http.StatusBadRequest,
		code,
		message,
		err,
	)
}

func (context *Context) limitBody() {
	if context.bodyLimited {
		return
	}
	context.request.Body = http.MaxBytesReader(
		context.response,
		context.request.Body,
		context.app.config.maxBodySize,
	)
	context.bodyLimited = true
}

func (context *Context) cleanup() {
	if context.request.MultipartForm == nil {
		return
	}
	if err := context.request.MultipartForm.RemoveAll(); err != nil {
		context.Logger().Warn("remove temporary multipart files", "error", err)
	}
}

type bindingField struct {
	index int
	name  string
}

type bindingMetadata struct {
	fields map[string][]bindingField
}

func bindValues(destination any, source, code, message string, values url.Values) error {
	return bindRequestValues(destination, source, code, message, func(name string) ([]string, bool) {
		raw, ok := values[name]
		return raw, ok
	})
}

func bindRequestValues(destination any, source, code, message string, lookup func(string) ([]string, bool)) error {
	value := reflect.ValueOf(destination)
	if destination == nil || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return bindingFault(code, message, fmt.Errorf("%s destination must be a non-nil pointer to a struct", source))
	}

	value = value.Elem()
	metadata := bindingMetadataFor(value.Type())
	for _, field := range metadata.fields[source] {
		raw, ok := lookup(field.name)
		if !ok {
			continue
		}
		if err := setField(value.Field(field.index), raw); err != nil {
			return bindingFault(code, message, &BindingError{
				Source: source,
				Field:  field.name,
				Cause:  err,
			})
		}
	}
	return nil
}

func bindingMetadataFor(valueType reflect.Type) *bindingMetadata {
	if cached, ok := bindingMetadataCache.Load(valueType); ok {
		return cached.(*bindingMetadata)
	}

	metadata := &bindingMetadata{fields: make(map[string][]bindingField, len(bindingSources))}
	for index := 0; index < valueType.NumField(); index++ {
		fieldType := valueType.Field(index)
		if !fieldType.IsExported() {
			continue
		}
		for _, source := range bindingSources {
			name, _, _ := strings.Cut(fieldType.Tag.Get(source), ",")
			if name == "" || name == "-" {
				continue
			}
			metadata.fields[source] = append(metadata.fields[source], bindingField{index: index, name: name})
		}
	}

	actual, _ := bindingMetadataCache.LoadOrStore(valueType, metadata)
	return actual.(*bindingMetadata)
}

func bindingFault(code, message string, err error) error {
	bindingErr := fault.Wrap(fault.InvalidArgument, code, message, err)
	var fieldErr *BindingError
	if errors.As(err, &fieldErr) {
		bindingErr.Fields = map[string]string{fieldErr.Field: "invalid value"}
	}
	return bindingErr
}

func setField(field reflect.Value, values []string) error {
	if field.Kind() == reflect.Slice {
		result := reflect.MakeSlice(field.Type(), len(values), len(values))
		for index, value := range values {
			if err := setScalar(result.Index(index), value); err != nil {
				return err
			}
		}
		field.Set(result)
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	return setScalar(field, values[0])
}

func setScalar(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(parsed)
	default:
		return fmt.Errorf("unsupported type %s", field.Type())
	}
	return nil
}
