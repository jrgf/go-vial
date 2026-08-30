package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial/testkit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testToken = "test-token"

func TestGRPCAndHTTPShareH2CListener(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	client := healthv1.NewHealthClient(dial(t, strings.TrimPrefix(server.URL, "http://"), insecure.NewCredentials()))
	contextValue, cancel := context.WithTimeout(authorized(context.Background()), 2*time.Second)
	defer cancel()

	response, err := client.Check(contextValue, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("health status=%s", response.Status)
	}
	if got := server.Do(server.NewRequest(http.MethodGet, "/healthz", nil)).StatusCode; got != http.StatusOK {
		t.Fatalf("HTTP status=%d", got)
	}
}

func TestGRPCMetadataStatusAndStreamingShutdown(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	client := healthv1.NewHealthClient(dial(t, strings.TrimPrefix(server.URL, "http://"), insecure.NewCredentials()))
	contextValue, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Check(contextValue, &healthv1.HealthCheckRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated call returned %v", err)
	}

	stream, err := client.Watch(authorized(contextValue), &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response, err := stream.Recv(); err != nil || response.Status != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("initial stream response=%v err=%v", response, err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream remained open after application shutdown")
	}
}

func TestGRPCOverTLS(t *testing.T) {
	app := newApp(testToken)
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(app)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	connection := dial(t, strings.TrimPrefix(server.URL, "https://"), credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: "example.com",
	}))
	client := healthv1.NewHealthClient(connection)
	contextValue, cancel := context.WithTimeout(authorized(context.Background()), 2*time.Second)
	defer cancel()
	if _, err := client.Check(contextValue, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
}

func dial(t *testing.T, address string, transportCredentials credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	return connection
}

func authorized(contextValue context.Context) context.Context {
	return metadata.AppendToOutgoingContext(contextValue, "authorization", "Bearer "+testToken)
}
