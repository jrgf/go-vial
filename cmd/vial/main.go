package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/jrgf/go-vial/internal/dev"
)

var version = "0.1.0"

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
  vial version

Examples:
  vial dev ./cmd/server
  vial dev --verbose ./examples/hello
  vial dev ./cmd/server -- --config ./config/dev.json

`, version)
}
