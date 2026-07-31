package dev

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultDebounce       = 250 * time.Millisecond
	DefaultRestartTimeout = 3 * time.Second
)

// Config controls the development build-and-restart loop.
type Config struct {
	Root           string
	Target         string
	AppArgs        []string
	Debounce       time.Duration
	RestartTimeout time.Duration
	Excludes       []string
	Verbose        bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Env            []string
}

func (config Config) withDefaults() (Config, error) {
	if config.Root == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return Config{}, err
		}
		config.Root = workingDirectory
	}
	absoluteRoot, err := filepath.Abs(config.Root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return Config{}, fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return Config{}, fmt.Errorf("project root is not a directory: %s", absoluteRoot)
	}
	config.Root = absoluteRoot

	if config.Target == "" {
		config.Target = "."
	}
	if config.Debounce <= 0 {
		config.Debounce = DefaultDebounce
	}
	if config.RestartTimeout <= 0 {
		config.RestartTimeout = DefaultRestartTimeout
	}
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	if config.Env == nil {
		config.Env = os.Environ()
	}
	return config, nil
}
