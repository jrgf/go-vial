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
)

const defaultMultipartMemory = int64(1 << 20) // 1 MiB

// BindQuery binds query parameters to primitive struct fields tagged with
// `query`. Repeated values bind to slices.
func (context *Context) BindQuery(destination any) error {
	if err := bindValues(destination, "query", context.request.URL.Query()); err != nil {
		return WrapHTTPError(
			http.StatusBadRequest,
			"invalid_query",
			"Query parameters contain invalid values",
			err,
		)
	}
	return nil
}

// BindForm binds URL-encoded or multipart form fields to primitive struct
// fields tagged with `form`. Repeated values bind to slices.
func (context *Context) BindForm(destination any) error {
	values, err := context.parseForm()
	if err != nil {
		return err
	}
	if err := bindValues(destination, "form", values); err != nil {
		return WrapHTTPError(
			http.StatusBadRequest,
			"invalid_form",
			"Form contains invalid values",
			err,
		)
	}
	return nil
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

func bindValues(destination any, tag string, values url.Values) error {
	value := reflect.ValueOf(destination)
	if destination == nil || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("%s destination must be a non-nil pointer to a struct", tag)
	}

	value = value.Elem()
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldType := valueType.Field(index)
		if !fieldType.IsExported() {
			continue
		}
		name := strings.Split(fieldType.Tag.Get(tag), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		raw, ok := values[name]
		if !ok {
			continue
		}
		if err := setField(value.Field(index), raw); err != nil {
			return fmt.Errorf("field %s: %w", fieldType.Name, err)
		}
	}
	return nil
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
