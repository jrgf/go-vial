package dev

import (
	"context"
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

	fmt.Fprintf(builder.config.Stdout, "[vial] building %s\n", builder.config.Target)
	command := exec.CommandContext(
		contextValue,
		"go",
		"build",
		"-o",
		outputPath,
		builder.config.Target,
	)
	command.Dir = builder.config.Root
	command.Env = builder.config.Env
	command.Stdout = builder.config.Stdout
	command.Stderr = builder.config.Stderr

	if err := command.Run(); err != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("go build: %w", err)
	}

	fmt.Fprintln(builder.config.Stdout, "[vial] build successful")
	return outputPath, nil
}
