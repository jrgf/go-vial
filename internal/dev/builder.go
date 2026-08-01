package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
)

// Builder compiles successive candidate executables to unique paths.
type Builder struct {
	config   Config
	sequence atomic.Uint64
}

func NewBuilder(config Config) *Builder {
	return &Builder{config: config}
}

func (builder *Builder) Build(contextValue context.Context) (string, error) {
	buildDirectory, buildTarget, err := ResolvePackage(builder.config.Root, builder.config.Target)
	if err != nil {
		return "", err
	}
	outputDirectory := filepath.Join(builder.config.Root, ".vial", "bin")
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return "", fmt.Errorf("create build directory: %w", err)
	}

	buildID := builder.sequence.Add(1)
	executableName := fmt.Sprintf(
		"app-%d-%d-%06d",
		os.Getpid(),
		time.Now().UnixNano(),
		buildID,
	)
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	outputPath := filepath.Join(outputDirectory, executableName)

	if _, err := fmt.Fprintf(builder.config.Stdout, "[vial] building %s\n", builder.config.Target); err != nil {
		return "", fmt.Errorf("write build status: %w", err)
	}
	command := exec.CommandContext(
		contextValue,
		"go",
		"build",
		"-o",
		outputPath,
		buildTarget,
	)
	command.Dir = buildDirectory
	command.Env = builder.config.Env
	command.Stdout = builder.config.Stdout
	command.Stderr = builder.config.Stderr

	if err := command.Run(); err != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("go build: %w", err)
	}

	if _, err := fmt.Fprintln(builder.config.Stdout, "[vial] build successful"); err != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("write build status: %w", err)
	}
	return outputPath, nil
}

// ResolvePackage finds the nearest Go module for a filesystem package target.
// Import paths and missing targets are left for the Go command to resolve.
func ResolvePackage(root, target string) (string, string, error) {
	if root == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("resolve working directory: %w", err)
		}
		root = workingDirectory
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve package root: %w", err)
	}
	if target == "" {
		target = "."
	}
	candidate := target
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absoluteRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	info, err := os.Stat(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return absoluteRoot, target, nil
		}
		return "", "", fmt.Errorf("inspect package target: %w", err)
	}
	if !info.IsDir() {
		return absoluteRoot, target, nil
	}

	for directory := candidate; ; directory = filepath.Dir(directory) {
		module, statErr := os.Stat(filepath.Join(directory, "go.mod"))
		if statErr == nil && !module.IsDir() {
			relative, relErr := filepath.Rel(directory, candidate)
			if relErr != nil {
				return "", "", fmt.Errorf("resolve package within module: %w", relErr)
			}
			if relative == "." {
				return directory, ".", nil
			}
			return directory, "." + string(filepath.Separator) + relative, nil
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", "", fmt.Errorf("inspect Go module: %w", statErr)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	return absoluteRoot, target, nil
}
