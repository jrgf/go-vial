package vialgrpc

import (
	"context"
	"errors"
	"net/http"
	"time"

	vial "github.com/jrgf/go-vial"
	"google.golang.org/grpc"
)

const (
	// DefaultMaxMessageBytes is the default send and receive message limit.
	DefaultMaxMessageBytes = 4 << 20
	// DefaultShutdownTimeout bounds grpc.Server.GracefulStop before Stop runs.
	DefaultShutdownTimeout = 5 * time.Second
)

// Config configures a gRPC integration module.
type Config struct {
	// Name is the Vial module name. An empty name uses "grpc".
	Name string
	// Pattern is the mounted HTTP pattern. An empty pattern uses "/".
	Pattern string
	// MaxReceiveBytes bounds incoming messages. Zero uses 4 MiB.
	MaxReceiveBytes int
	// MaxSendBytes bounds outgoing messages. Zero uses 4 MiB.
	MaxSendBytes int
	// ShutdownTimeout bounds GracefulStop. Zero uses five seconds.
	ShutdownTimeout time.Duration
	// ServerOptions configures grpc.NewServer before enforced message limits.
	ServerOptions []grpc.ServerOption
}

// Module owns one grpc-go server mounted in a Vial application.
type Module struct {
	name            string
	pattern         string
	shutdownTimeout time.Duration
	server          *grpc.Server
}

// New creates a gRPC module with bounded send and receive messages.
func New(config Config) (*Module, error) {
	if config.MaxReceiveBytes < 0 {
		return nil, errors.New("vialgrpc: receive limit cannot be negative")
	}
	if config.MaxSendBytes < 0 {
		return nil, errors.New("vialgrpc: send limit cannot be negative")
	}
	if config.ShutdownTimeout < 0 {
		return nil, errors.New("vialgrpc: shutdown timeout cannot be negative")
	}
	if config.Name == "" {
		config.Name = "grpc"
	}
	if config.Pattern == "" {
		config.Pattern = "/"
	}
	if config.MaxReceiveBytes == 0 {
		config.MaxReceiveBytes = DefaultMaxMessageBytes
	}
	if config.MaxSendBytes == 0 {
		config.MaxSendBytes = DefaultMaxMessageBytes
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}

	options := append([]grpc.ServerOption(nil), config.ServerOptions...)
	options = append(options,
		grpc.MaxRecvMsgSize(config.MaxReceiveBytes),
		grpc.MaxSendMsgSize(config.MaxSendBytes),
	)
	return &Module{
		name:            config.Name,
		pattern:         config.Pattern,
		shutdownTimeout: config.ShutdownTimeout,
		server:          grpc.NewServer(options...),
	}, nil
}

// Name implements vial.Module.
func (module *Module) Name() string { return module.name }

// Server returns the grpc-go server for service registration.
func (module *Module) Server() *grpc.Server { return module.server }

// Register mounts the gRPC handler and registers its shutdown task.
func (module *Module) Register(registrar *vial.Registrar) error {
	registrar.HandleHTTP(module.pattern, module.server)
	registrar.Go("shutdown", module.shutdown)
	return nil
}

func (module *Module) shutdown(contextValue context.Context) error {
	<-contextValue.Done()
	done := make(chan struct{})
	go func() {
		module.server.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(module.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		module.server.Stop()
		<-done
		return nil
	}
}

// H2CProtocols enables HTTP/1, HTTP/2, and unencrypted HTTP/2 on a Vial
// listener. Use it only behind a trusted network boundary or TLS terminator.
func H2CProtocols() *http.Protocols {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return protocols
}
