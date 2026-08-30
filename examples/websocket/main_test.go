package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jrgf/go-vial/testkit"
)

const testToken = "test-token"

func TestWebSocketEchoUsesVialMiddleware(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	contextValue, cancel := testContext(t)
	defer cancel()

	connection, response, err := websocket.Dial(contextValue, websocketURL(server.URL), nil)
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized dial: response=%v error=%v", response, err)
	}

	connection = dialAuthorized(t, server.URL, nil)
	defer func() { _ = connection.CloseNow() }()
	assertEcho(t, connection, "hello")
}

func TestWebSocketRejectsMalformedHandshakeAndCrossOrigin(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/ws", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatalf("malformed handshake: %v", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()
	if response.StatusCode < http.StatusBadRequest {
		t.Fatalf("malformed handshake status = %d", response.StatusCode)
	}

	header := make(http.Header)
	header.Set("Origin", "https://evil.example")
	contextValue, cancel := testContext(t)
	defer cancel()
	connection, response, err := websocket.Dial(contextValue, websocketURL(server.URL), &websocket.DialOptions{
		HTTPHeader: authorizedHeader(header),
	})
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial: response=%v error=%v", response, err)
	}
}

func TestWebSocketEnforcesMessageLimit(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	connection := dialAuthorized(t, server.URL, nil)
	defer func() { _ = connection.CloseNow() }()
	contextValue, cancel := testContext(t)
	defer cancel()

	if err := connection.Write(contextValue, websocket.MessageText, []byte(strings.Repeat("x", maximumMessageBytes+1))); err != nil {
		t.Fatalf("write oversized message: %v", err)
	}
	if _, _, err := connection.Read(contextValue); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("close status = %d, want %d; error=%v", websocket.CloseStatus(err), websocket.StatusMessageTooBig, err)
	}
}

func TestWebSocketConnectionsAreConcurrent(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	for index := range 8 {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			connection := dialAuthorized(t, server.URL, nil)
			defer func() { _ = connection.CloseNow() }()
			assertEcho(t, connection, fmt.Sprintf("client-%d", index))
		})
	}
}

func TestWebSocketClosesDuringApplicationShutdown(t *testing.T) {
	server := testkit.Start(t, newApp(testToken))
	connection := dialAuthorized(t, server.URL, nil)
	defer func() { _ = connection.CloseNow() }()

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	contextValue, cancel := testContext(t)
	defer cancel()
	if _, _, err := connection.Read(contextValue); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("close status = %d, want %d; error=%v", websocket.CloseStatus(err), websocket.StatusGoingAway, err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close server: %v", err)
		}
	case <-contextValue.Done():
		t.Fatal("server did not stop")
	}
}

func dialAuthorized(t *testing.T, serverURL string, header http.Header) *websocket.Conn {
	t.Helper()
	contextValue, cancel := testContext(t)
	defer cancel()
	connection, response, err := websocket.Dial(contextValue, websocketURL(serverURL), &websocket.DialOptions{
		HTTPHeader: authorizedHeader(header),
	})
	if err != nil {
		t.Fatalf("dial WebSocket: response=%v error=%v", response, err)
	}
	return connection
}

func assertEcho(t *testing.T, connection *websocket.Conn, message string) {
	t.Helper()
	contextValue, cancel := testContext(t)
	defer cancel()
	if err := connection.Write(contextValue, websocket.MessageText, []byte(message)); err != nil {
		t.Fatalf("write: %v", err)
	}
	messageType, received, err := connection.Read(contextValue)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if messageType != websocket.MessageText || string(received) != message {
		t.Fatalf("echo = type %d %q, want text %q", messageType, received, message)
	}
}

func authorizedHeader(header http.Header) http.Header {
	if header == nil {
		header = make(http.Header)
	}
	header.Set("Authorization", "Bearer "+testToken)
	return header
}

func websocketURL(serverURL string) string {
	return "ws" + strings.TrimPrefix(serverURL, "http") + "/ws"
}

func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), 2*time.Second)
}
