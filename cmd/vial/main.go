package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/internal/dev"
	"github.com/jrgf/go-vial/internal/load"
)

var version = "0.12.0"

const routesOutputEnvironment = "VIAL_ROUTES_OUTPUT"

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vial:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage()
		return nil
	}

	switch arguments[0] {
	case "dev":
		return runDev(arguments[1:])
	case "routes":
		return runRoutes(arguments[1:], os.Stdout)
	case "doctor":
		return runDoctor(arguments[1:], os.Stdout)
	case "load":
		return runLoad(arguments[1:], os.Stdout, os.Stderr)
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
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

	ticker := time.NewTicker(time.Second)
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
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "Usage: vial doctor [package] [-- application arguments]")
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
	if _, err := fmt.Fprintf(output, "vial doctor: ok (routes: %d)\n", len(routes)); err != nil {
		return fmt.Errorf("write doctor result: %w", err)
	}
	return nil
}

func inspectApplication(target string, applicationArguments []string) ([]vial.Route, error) {
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
	command.Env = append(os.Environ(), routesOutputEnvironment+"="+outputPath)
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
	var routes []vial.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, fmt.Errorf("decode inspection output: %w", err)
	}
	return routes, nil
}

func writeRoutes(output io.Writer, routes []vial.Route, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(routes)
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
  vial dev [flags] [package] [-- application arguments]
  vial routes [--json] [package] [-- application arguments]
  vial doctor [package] [-- application arguments]
  vial load [flags] URL
  vial version

Examples:
  vial dev ./cmd/server
  vial dev --verbose ./examples/hello
  vial dev ./cmd/server -- --config ./config/dev.json
  vial routes ./examples/hello
  vial doctor ./examples/hello
  vial load --workers 100 --duration 10s http://localhost:8080/

`, version)
}
