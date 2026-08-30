package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/internal/dev"
	"github.com/jrgf/go-vial/internal/load"
)

var (
	version              = "0.18.0"
	commit               = "development"
	buildGoVersion       string
	loadProgressInterval = time.Second
)

const (
	routesOutputEnvironment         = "VIAL_ROUTES_OUTPUT"
	httpInspectionOutputEnvironment = "VIAL_HTTP_INSPECTION_OUTPUT"
	httpInspectionPathEnvironment   = "VIAL_HTTP_INSPECTION_PATH"
)

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if code := commandExitCode(run(os.Args[1:]), os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func commandExitCode(err error, output io.Writer) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	_, _ = fmt.Fprintln(output, "vial:", err)
	return 1
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage()
		return nil
	}

	switch arguments[0] {
	case "new":
		return runNew(arguments[1:], os.Stdout)
	case "dev":
		return runDev(arguments[1:])
	case "routes":
		return runRoutes(arguments[1:], os.Stdout)
	case "doctor":
		return runDoctor(arguments[1:], os.Stdout)
	case "config":
		return runConfig(arguments[1:], os.Stdout)
	case "openapi":
		return runOpenAPI(arguments[1:], os.Stdout)
	case "load":
		return runLoad(arguments[1:], os.Stdout, os.Stderr)
	case "version", "--version", "-v":
		return printVersion(arguments[1:], os.Stdout)
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func printVersion(arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		_, err := fmt.Fprintln(output, version)
		return err
	}
	if len(arguments) == 1 && (arguments[0] == "--help" || arguments[0] == "-h") {
		_, err := fmt.Fprintln(output, "Usage: vial version [--verbose|--json]")
		return err
	}
	goVersion := buildGoVersion
	if goVersion == "" {
		goVersion = runtime.Version()
	}
	if len(arguments) == 1 && arguments[0] == "--json" {
		return writeJSON(output, map[string]string{
			"version": version,
			"commit":  commit,
			"go":      goVersion,
		})
	}
	if len(arguments) != 1 || arguments[0] != "--verbose" {
		return fmt.Errorf("usage: vial version [--verbose|--json]")
	}
	_, err := fmt.Fprintf(output, "version=%s\ncommit=%s\ngo=%s\n", version, commit, goVersion)
	return err
}

func runLoad(arguments []string, output, progressOutput io.Writer) error {
	flags := flag.NewFlagSet("vial load", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workers := flags.Int("workers", 50, "number of concurrent workers")
	duration := flags.Duration("duration", 10*time.Second, "time to start requests")
	timeout := flags.Duration("timeout", 5*time.Second, "timeout for each request")
	maxErrorRate := flags.Float64("max-error-rate", -1, "maximum error percentage; disabled by default")
	maxP95 := flags.Duration("max-p95", 0, "maximum p95 latency; disabled by default")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial load [flags] URL")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("expected one URL, received %d", flags.NArg())
	}

	contextValue, stop := signal.NotifyContext(context.Background(), developmentSignals()...)
	defer stop()
	config := load.Config{
		URL:      flags.Arg(0),
		Workers:  *workers,
		Duration: *duration,
		Timeout:  *timeout,
	}
	var result load.Result
	finished := make(chan error, 1)
	started := time.Now()
	go func() {
		var err error
		result, err = load.Run(contextValue, config)
		finished <- err
	}()

	ticker := time.NewTicker(loadProgressInterval)
	defer ticker.Stop()
	progressComplete := false
	var err error
running:
	for {
		select {
		case err = <-finished:
			break running
		case now := <-ticker.C:
			if config.Duration <= 0 {
				continue
			}
			elapsed := min(now.Sub(started), config.Duration)
			progressComplete = elapsed == config.Duration
			percent := int(float64(elapsed) / float64(config.Duration) * 100)
			if _, writeErr := fmt.Fprintf(progressOutput, "[vial] load progress: %d%% (%s/%s)\n", percent, elapsed.Truncate(time.Second), config.Duration); writeErr != nil {
				stop()
				<-finished
				return fmt.Errorf("write load progress: %w", writeErr)
			}
			if progressComplete {
				ticker.Stop()
			}
		}
	}
	if err == nil && !progressComplete {
		if _, writeErr := fmt.Fprintf(progressOutput, "[vial] load progress: 100%% (%s/%s)\n", config.Duration, config.Duration); writeErr != nil {
			return fmt.Errorf("write load progress: %w", writeErr)
		}
	}
	if err == nil || result.Requests > 0 {
		if writeErr := load.WriteSummary(output, result); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return err
	}
	return load.Check(result, load.Thresholds{MaxErrorRate: *maxErrorRate, MaxP95: *maxP95})
}

func runDev(arguments []string) error {
	frameworkArguments, applicationArguments := splitApplicationArguments(arguments)

	flags := flag.NewFlagSet("vial dev", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	var excludes stringList
	root := flags.String("root", "", "project root to watch and build from")
	debounce := flags.Duration("debounce", dev.DefaultDebounce, "source-change debounce duration")
	restartTimeout := flags.Duration("restart-timeout", dev.DefaultRestartTimeout, "graceful child shutdown timeout")
	verbose := flags.Bool("verbose", false, "print every relevant changed path")
	flags.Var(&excludes, "exclude", "additional directory or path to ignore; repeatable")

	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial dev [flags] [package] [-- application arguments]")
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

	contextValue, stop := signal.NotifyContext(
		context.Background(),
		developmentSignals()...,
	)
	defer stop()

	runner, err := dev.NewRunner(dev.Config{
		Root:           *root,
		Target:         target,
		AppArgs:        applicationArguments,
		Debounce:       *debounce,
		RestartTimeout: *restartTimeout,
		Excludes:       excludes,
		Verbose:        *verbose,
	})
	if err != nil {
		return err
	}

	return runner.Run(contextValue)
}

func runRoutes(arguments []string, output io.Writer) error {
	frameworkArguments, applicationArguments := splitApplicationArguments(arguments)
	flags := flag.NewFlagSet("vial routes", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print routes as JSON")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial routes [--json] [package] [-- application arguments]")
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

	routes, err := inspectApplication(target, applicationArguments)
	if err != nil {
		return err
	}
	return writeRoutes(output, routes, *jsonOutput)
}

func runDoctor(arguments []string, output io.Writer) error {
	frameworkArguments, applicationArguments := splitApplicationArguments(arguments)
	flags := flag.NewFlagSet("vial doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "print diagnostics as JSON")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial doctor [--json] [package] [-- application arguments]")
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
	routes, err := inspectApplication(target, applicationArguments)
	if err != nil {
		return err
	}
	if *jsonOutput {
		named := 0
		for _, route := range routes {
			if route.Name != "" {
				named++
			}
		}
		return writeJSON(output, map[string]any{
			"ok":           true,
			"routes":       len(routes),
			"named_routes": named,
			"go":           runtime.Version(),
		})
	}
	if _, err := fmt.Fprintf(output, "vial doctor: ok (routes: %d)\n", len(routes)); err != nil {
		return fmt.Errorf("write doctor result: %w", err)
	}
	return nil
}

func inspectApplication(target string, applicationArguments []string) ([]vial.Route, error) {
	data, err := inspectOutput(target, applicationArguments, routesOutputEnvironment)
	if err != nil {
		return nil, err
	}
	var routes []vial.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, fmt.Errorf("decode inspection output: %w", err)
	}
	return routes, nil
}

func inspectOutput(target string, applicationArguments []string, outputEnvironment string, extraEnvironment ...string) ([]byte, error) {
	workingDirectory, resolvedTarget, err := dev.ResolvePackage("", target)
	if err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp("", "vial-inspect-*.json")
	if err != nil {
		return nil, fmt.Errorf("create inspection output: %w", err)
	}
	outputPath := temporary.Name()
	defer func() { _ = os.Remove(outputPath) }()
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close inspection output: %w", err)
	}

	commandArguments := append([]string{"run", resolvedTarget}, applicationArguments...)
	command := exec.Command("go", commandArguments...)
	command.Dir = workingDirectory
	command.Env = inspectionEnvironment(os.Environ())
	command.Env = append(command.Env, outputEnvironment+"="+outputPath)
	command.Env = append(command.Env, extraEnvironment...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("inspect application: %w", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read inspection output: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("application did not call App.Run; use App.Routes directly")
	}
	return data, nil
}

func inspectionEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, value := range environment {
		if strings.HasPrefix(value, routesOutputEnvironment+"=") ||
			strings.HasPrefix(value, httpInspectionOutputEnvironment+"=") ||
			strings.HasPrefix(value, httpInspectionPathEnvironment+"=") {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeRoutes(output io.Writer, routes []vial.Route, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(output, routes)
	}

	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "METHOD\tPATH\tNAME\tMODULE"); err != nil {
		return fmt.Errorf("write route table: %w", err)
	}
	for _, route := range routes {
		method := route.Method
		if method == "" {
			method = "*"
		}
		name := route.Name
		if name == "" {
			name = "-"
		}
		module := route.Module
		if module == "" {
			module = "-"
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", method, route.Path, name, module); err != nil {
			return fmt.Errorf("write route table: %w", err)
		}
	}
	return table.Flush()
}

func splitApplicationArguments(arguments []string) ([]string, []string) {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[:index], arguments[index+1:]
		}
	}
	return arguments, nil
}

func printUsage() {
	fmt.Printf(`vial %s

Usage:
  vial new [--module path] [--json] directory
  vial dev [flags] [package] [-- application arguments]
  vial routes [--json] [package] [-- application arguments]
  vial doctor [--json] [package] [-- application arguments]
  vial config [--json] [package] [-- application arguments]
  vial openapi [--path path] [--output file] [package] [-- application arguments]
  vial load [flags] URL
  vial version [--verbose|--json]

Examples:
  vial new --module example.com/service ./service
  vial dev ./cmd/server
  vial dev --verbose ./examples/hello
  vial dev ./cmd/server -- --config ./config/dev.json
  vial routes ./examples/hello
  vial doctor ./examples/hello
  vial config --json ./examples/config
  vial openapi --output openapi.json ./examples/openapi
  vial load --workers 100 --duration 10s http://localhost:8080/

`, version)
}
