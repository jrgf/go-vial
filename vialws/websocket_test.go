package vialws_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
	"github.com/jrgf/go-vial/vialws"
)

func TestHandlerLimitsMessagesAndClosesOnShutdown(t *testing.T) {
	handler, err := vialws.NewHandler(vialws.Config{
		ReadLimit: 32,
		Handler: func(contextValue context.Context, connection *websocket.Conn, _ *http.Request) {
			for {
				messageType, message, err := connection.Read(contextValue)
				if err != nil {
					return
				}
				if err := connection.Write(contextValue, messageType, message); err != nil {
					return
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.HandleHTTP("GET /ws", handler)
	server := testkit.Start(t, app)

	connection := dial(t, server.URL)
	if err := connection.Write(t.Context(), websocket.MessageText, []byte(strings.Repeat("x", 33))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.Read(t.Context()); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("oversized close status=%d err=%v", websocket.CloseStatus(err), err)
	}
	_ = connection.CloseNow()

	connection = dial(t, server.URL)
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	readContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := connection.Read(readContext); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("shutdown close status=%d err=%v", websocket.CloseStatus(err), err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestHandlerValidatesConfiguration(t *testing.T) {
	if _, err := vialws.NewHandler(vialws.Config{}); err == nil {
		t.Fatal("expected nil handler error")
	}
	if _, err := vialws.NewHandler(vialws.Config{Handler: func(context.Context, *websocket.Conn, *http.Request) {}, ReadLimit: -1}); err == nil {
		t.Fatal("expected read limit error")
	}
}

func dial(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	contextValue, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(contextValue, "ws"+strings.TrimPrefix(serverURL, "http")+"/ws", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}
