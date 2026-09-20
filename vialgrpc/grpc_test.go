package vialgrpc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
	"github.com/jrgf/go-vial/vialgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestModuleServesLimitsAndStopsGRPC(t *testing.T) {
	module, err := vialgrpc.New(vialgrpc.Config{
		MaxReceiveBytes: 128,
		MaxSendBytes:    128,
	})
	if err != nil {
		t.Fatal(err)
	}
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(module.Server(), healthServer)

	app := vial.New(vial.WithHTTPProtocols(vialgrpc.H2CProtocols()))
	if err := app.Register(module); err != nil {
		t.Fatal(err)
	}
	server := testkit.Start(t, app)
	connection, err := grpc.NewClient(
		"passthrough:///"+strings.TrimPrefix(server.URL, "http://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := healthv1.NewHealthClient(connection)

	contextValue, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := client.Check(contextValue, &healthv1.HealthCheckRequest{})
	if err != nil || response.Status != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("health response=%v err=%v", response, err)
	}
	_, err = client.Check(contextValue, &healthv1.HealthCheckRequest{Service: strings.Repeat("x", 256)})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized request code=%s err=%v", status.Code(err), err)
	}

	stream, err := client.Watch(contextValue, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("gRPC stream remained open after shutdown")
	}
}

func TestModuleValidatesConfiguration(t *testing.T) {
	for _, config := range []vialgrpc.Config{
		{MaxReceiveBytes: -1},
		{MaxSendBytes: -1},
		{ShutdownTimeout: -1},
	} {
		if _, err := vialgrpc.New(config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}
}
