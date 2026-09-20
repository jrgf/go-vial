package vialgrpc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
	"github.com/jrgf/go-vial/vialgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

type unwrapOnly struct{ http.ResponseWriter }

func (w unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestGRPCStreamSurvivesHTTPTimeouts(t *testing.T) {
	m, err := vialgrpc.New(vialgrpc.Config{ServerOptions: []grpc.ServerOption{grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		time.Sleep(300 * time.Millisecond)
		return handler(ctx, req)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	healthServer := health.NewServer()
	healthv1.RegisterHealthServer(m.Server(), healthServer)
	echo := grpc.StreamDesc{StreamName: "Chat", Handler: echoMessages, ServerStreams: true, ClientStreams: true}
	m.Server().RegisterService(&grpc.ServiceDesc{ServiceName: "deadline.Echo", HandlerType: (*any)(nil), Streams: []grpc.StreamDesc{echo}}, nil)
	app := vial.New(vial.WithHTTPProtocols(vialgrpc.H2CProtocols()), vial.WithWriteTimeout(100*time.Millisecond), vial.WithReadTimeout(100*time.Millisecond), vial.WithReadHeaderTimeout(100*time.Millisecond))
	app.UseHTTP(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(unwrapOnly{w}, r) })
	})
	if err := app.Register(m); err != nil {
		t.Fatal(err)
	}
	server := testkit.Start(t, app)
	conn, err := grpc.NewClient("passthrough:///"+strings.TrimPrefix(server.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := healthv1.NewHealthClient(conn).Watch(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	chat, err := conn.NewStream(ctx, &echo, "/deadline.Echo/Chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.SendMsg(&healthv1.HealthCheckRequest{Service: "first"}); err != nil {
		t.Fatal(err)
	}
	var echoed healthv1.HealthCheckRequest
	if err := chat.RecvMsg(&echoed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := chat.SendMsg(&healthv1.HealthCheckRequest{Service: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := chat.RecvMsg(&echoed); err != nil || echoed.Service != "second" {
		t.Fatalf("client stream body after read deadline: %v, %v", &echoed, err)
	}
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
	r, err := stream.Recv()
	if err != nil || r.GetStatus() != healthv1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("response=%v err=%v", r, err)
	}
	if _, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatalf("slow unary call: %v", err)
	}
}

func echoMessages(_ any, stream grpc.ServerStream) error {
	for {
		var request healthv1.HealthCheckRequest
		if err := stream.RecvMsg(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := stream.SendMsg(&request); err != nil {
			return err
		}
	}
}
