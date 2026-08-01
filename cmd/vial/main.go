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

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/internal/dev"
)

var version = "0.3.0"

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
		fmt.Fprintln(flags.Output(), "Usage: vial dev [flags] [package] [-- application arguments]")
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
		fmt.Fprintln(flags.Output(), "Usage: vial routes [--json] [package] [-- application arguments]")
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

	temporary, err := os.CreateTemp("", "vial-routes-*.json")
	if err != nil {
		return fmt.Errorf("create route output: %w", err)
	}
	outputPath := temporary.Name()
	defer os.Remove(outputPath)
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close route output: %w", err)
	}

	commandArguments := append([]string{"run", target}, applicationArguments...)
	command := exec.Command("go", commandArguments...)
	command.Env = append(os.Environ(), routesOutputEnvironment+"="+outputPath)
	command.Stdin = os.Stdin
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("inspect routes: %w", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("read routes: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("application did not call App.Run; use App.Routes directly")
	}
	var routes []vial.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return fmt.Errorf("decode routes: %w", err)
	}
	return writeRoutes(output, routes, *jsonOutput)
}

func writeRoutes(output io.Writer, routes []vial.Route, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(routes)
	}

	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "METHOD\tPATH\tNAME\tMODULE")
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
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", method, route.Path, name, module)
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
  vial version

Examples:
  vial dev ./cmd/server
  vial dev --verbose ./examples/hello
  vial dev ./cmd/server -- --config ./config/dev.json
  vial routes ./examples/hello

`, version)
}
