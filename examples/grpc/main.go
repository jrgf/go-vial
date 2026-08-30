package main

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"os"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/vialgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

const maxMessageSize = 64 << 10

func main() {
	token := os.Getenv("VIAL_GRPC_TOKEN")
	if token == "" {
		log.Fatal("VIAL_GRPC_TOKEN is required")
	}

	log.Print("HTTP and gRPC listening on http://localhost:8080")
	app, err := newApp(token)
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		log.Fatal(err)
	}
}

func newApp(token string) (*vial.App, error) {
	app := vial.New(vial.WithHTTPProtocols(vialgrpc.H2CProtocols()))
	app.Get("/healthz", func(contextValue *vial.Context) error {
		return contextValue.Text(http.StatusOK, "ok\n")
	})

	module, err := vialgrpc.New(vialgrpc.Config{
		MaxReceiveBytes: maxMessageSize,
		MaxSendBytes:    maxMessageSize,
		ServerOptions: []grpc.ServerOption{
			grpc.UnaryInterceptor(authenticateUnary(token)),
			grpc.StreamInterceptor(authenticateStream(token)),
		},
	})
	if err != nil {
		return nil, err
	}
	server := module.Server()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(server, healthServer)
	reflection.Register(server)
	if err := app.Register(module); err != nil {
		return nil, err
	}
	return app, nil
}

func authenticateUnary(token string) grpc.UnaryServerInterceptor {
	return func(contextValue context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authenticate(contextValue, token); err != nil {
			return nil, err
		}
		return handler(contextValue, request)
	}
}

func authenticateStream(token string) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authenticate(stream.Context(), token); err != nil {
			return err
		}
		return handler(server, stream)
	}
}

func authenticate(contextValue context.Context, token string) error {
	values := metadata.ValueFromIncomingContext(contextValue, "authorization")
	if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+token)) != 1 {
		return status.Error(codes.Unauthenticated, "invalid credentials")
	}
	return nil
}
