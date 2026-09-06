package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const generatedGoVersion = "1.26.6"

func runConfig(arguments []string, output io.Writer) error {
	// ponytail: application config is arbitrary, so this command validates it
	// without exposing secrets; applications can add their own redacted view.
	frameworkArguments, applicationArguments := splitApplicationArguments(arguments)
	flags := flag.NewFlagSet("vial config", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print the result as JSON")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial config [--json] [package] [-- application arguments]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(frameworkArguments); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("expected at most one Go package, received %d", flags.NArg())
	}
	target := "."
	if flags.NArg() == 1 {
		target = flags.Arg(0)
	}
	if _, err := inspectApplication(target, applicationArguments); err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(output, map[string]bool{"valid": true})
	}
	if _, err := fmt.Fprintln(output, "vial config: ok"); err != nil {
		return fmt.Errorf("write config result: %w", err)
	}
	return nil
}

func runOpenAPI(arguments []string, output io.Writer) error {
	frameworkArguments, applicationArguments := splitApplicationArguments(arguments)
	flags := flag.NewFlagSet("vial openapi", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	documentPath := flags.String("path", "/openapi.json", "application path that serves OpenAPI JSON")
	outputPath := flags.String("output", "-", "output file; - writes to stdout")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial openapi [--path path] [--output file] [package] [-- application arguments]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(frameworkArguments); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("expected at most one Go package, received %d", flags.NArg())
	}
	if *outputPath == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	target := "."
	if flags.NArg() == 1 {
		target = flags.Arg(0)
	}
	data, err := inspectOutput(
		target,
		applicationArguments,
		httpInspectionOutputEnvironment,
		httpInspectionPathEnvironment+"="+*documentPath,
	)
	if err != nil {
		return err
	}
	var header struct {
		OpenAPI string `json:"openapi"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("decode OpenAPI document: %w", err)
	}
	if !strings.HasPrefix(header.OpenAPI, "3.1.") {
		return fmt.Errorf("endpoint returned OpenAPI version %q; version 3.1 is required", header.OpenAPI)
	}
	if *outputPath != "-" {
		if err := os.WriteFile(*outputPath, data, 0o644); err != nil {
			return fmt.Errorf("write OpenAPI document: %w", err)
		}
		return nil
	}
	written, err := output.Write(data)
	if err != nil {
		return fmt.Errorf("write OpenAPI document: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("write OpenAPI document: %w", io.ErrShortWrite)
	}
	return nil
}

func runNew(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("vial new", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	modulePath := flags.String("module", "", "Go module path; defaults to the directory name")
	jsonOutput := flags.Bool("json", false, "print the result as JSON")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial new [--module path] [--json] directory")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("expected one project directory, received %d", flags.NArg())
	}

	directory, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return fmt.Errorf("resolve project directory: %w", err)
	}
	if _, err := os.Stat(directory); err == nil {
		return fmt.Errorf("project directory %q already exists", directory)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect project directory: %w", err)
	}
	module := *modulePath
	if module == "" {
		module = filepath.Base(directory)
	}
	if !validModulePath(module) {
		return fmt.Errorf("invalid Go module path %q", module)
	}

	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create project parent: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".vial-new-*")
	if err != nil {
		return fmt.Errorf("create project staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	moduleVersion := "v" + strings.TrimPrefix(version, "v")
	goMod := fmt.Sprintf("module %s\n\ngo %s\n\nrequire github.com/jrgf/go-vial %s\n", module, generatedGoVersion, moduleVersion)
	for name, contents := range map[string]string{
		"go.mod":  goMod,
		"main.go": generatedMain,
	} {
		if err := os.WriteFile(filepath.Join(temporary, name), []byte(contents), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if err := os.Rename(temporary, directory); err != nil {
		return fmt.Errorf("create project: %w", err)
	}

	result := struct {
		Directory string `json:"directory"`
		Module    string `json:"module"`
		Go        string `json:"go"`
		Vial      string `json:"vial"`
	}{Directory: directory, Module: module, Go: generatedGoVersion, Vial: moduleVersion}
	if *jsonOutput {
		return writeJSON(output, result)
	}
	if _, err := fmt.Fprintf(output, "created %s\n", directory); err != nil {
		return fmt.Errorf("write new project result: %w", err)
	}
	return nil
}

func validModulePath(module string) bool {
	// ponytail: keep scaffolding dependency-free; use x/mod/module.CheckPath if
	// support for every escaped module-path character becomes necessary.
	if module == "" || module != strings.TrimSpace(module) {
		return false
	}
	for _, character := range module {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("-._~/", character) {
			continue
		}
		return false
	}
	for segment := range strings.SplitSeq(module, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

const generatedMain = `package main

import (
	"context"
	"log"
	"net/http"
	"os"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

func main() {
	app := vial.New()
	app.Use(middleware.RequestID(), middleware.Logger(), middleware.Recover(), middleware.SecurityHeaders())
	app.Health("/live")
	app.Readiness("/ready")
	app.Get("/", func(contextValue *vial.Context) error {
		return contextValue.JSON(http.StatusOK, map[string]string{"message": "hello"})
	}, vial.RouteName("home"))

	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	if err := app.Run(context.Background(), address); err != nil {
		log.Fatal(err)
	}
}
`
