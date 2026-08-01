// Package config loads typed application configuration from explicit files and
// environment variables.
package config

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	durationType        = reflect.TypeOf(time.Duration(0))
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// Validator validates a fully loaded configuration. Implementations should not
// include secrets in returned errors.
type Validator interface {
	Validate() error
}

// Option configures Load.
type Option func(*settings)

type settings struct {
	files   []fileSource
	environ []string
}

type fileSource struct {
	path     string
	optional bool
}

// File loads a required JSON file. Files are applied in registration order.
func File(path string) Option {
	return func(settings *settings) {
		settings.files = append(settings.files, fileSource{path: path})
	}
}

// OptionalFile loads a JSON file when it exists.
func OptionalFile(path string) Option {
	return func(settings *settings) {
		settings.files = append(settings.files, fileSource{path: path, optional: true})
	}
}

// Environ replaces the process environment used by Load. It is primarily
// useful for deterministic tests and embedded applications.
func Environ(values []string) Option {
	copied := append([]string(nil), values...)
	return func(settings *settings) {
		settings.environ = append([]string(nil), copied...)
	}
}

// Load applies JSON files and then environment variables to destination.
// Existing field values act as defaults. Destination must be a non-nil pointer
// to a struct. Loader-generated errors never include configuration values.
func Load(destination any, options ...Option) error {
	value := reflect.ValueOf(destination)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return errors.New("config: destination must be a non-nil pointer to a struct")
	}

	configuration := settings{environ: os.Environ()}
	for _, option := range options {
		if option != nil {
			option(&configuration)
		}
	}

	for _, source := range configuration.files {
		if source.path == "" {
			return errors.New("config: file path cannot be empty")
		}
		if err := loadJSON(destination, source); err != nil {
			return err
		}
	}
	if err := loadEnvironment(value.Elem(), configuration.environ); err != nil {
		return err
	}
	if validator, ok := destination.(Validator); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("config: validation failed: %w", err)
		}
	}
	return nil
}

func loadJSON(destination any, source fileSource) (resultErr error) {
	file, err := os.Open(source.path)
	if err != nil {
		if source.optional && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: open file %q: %w", source.path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("config: close file %q: %w", source.path, err))
		}
	}()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("config: decode file %q: %w", source.path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("config: decode file %q: multiple JSON values", source.path)
		}
		return fmt.Errorf("config: decode file %q: %w", source.path, err)
	}
	return nil
}

func loadEnvironment(destination reflect.Value, environ []string) error {
	values := make(map[string]string, len(environ))
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return loadStructEnvironment(destination, values, "")
}

func loadStructEnvironment(destination reflect.Value, environ map[string]string, prefix string) error {
	destinationType := destination.Type()
	for index := 0; index < destination.NumField(); index++ {
		fieldType := destinationType.Field(index)
		if !fieldType.IsExported() {
			continue
		}

		field := destination.Field(index)
		fieldPath := fieldType.Name
		if prefix != "" {
			fieldPath = prefix + "." + fieldPath
		}

		name, tagged := fieldType.Tag.Lookup("env")
		if tagged {
			if name == "-" {
				continue
			}
			if name == "" {
				return fmt.Errorf("config: field %s has an empty environment variable name", fieldPath)
			}
			value, exists := environ[name]
			if !exists {
				continue
			}
			if err := setText(field, value); err != nil {
				return fmt.Errorf(
					"config: environment variable %s for field %s is not valid for %s",
					name,
					fieldPath,
					field.Type(),
				)
			}
			continue
		}

		switch field.Kind() {
		case reflect.Struct:
			if err := loadStructEnvironment(field, environ, fieldPath); err != nil {
				return err
			}
		case reflect.Pointer:
			if !field.IsNil() && field.Elem().Kind() == reflect.Struct {
				if err := loadStructEnvironment(field.Elem(), environ, fieldPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func setText(field reflect.Value, value string) error {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		if field.CanInterface() && field.Type().Implements(textUnmarshalerType) {
			return field.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(value))
		}
		return setText(field.Elem(), value)
	}
	if field.CanAddr() && field.Addr().Type().Implements(textUnmarshalerType) {
		return field.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(value))
	}
	if field.Type() == durationType {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		field.SetInt(int64(parsed))
		return nil
	}

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
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
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
