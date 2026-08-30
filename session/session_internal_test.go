package session

import (
	"net/http"
	"testing"
	"time"
)

func TestEncryptedCookieExpiryAndNonceUniqueness(t *testing.T) {
	manager, err := New(Config{
		Keys:                            [][]byte{[]byte("0123456789abcdef0123456789abcdef")},
		MaxAge:                          time.Minute,
		DangerouslyAllowInsecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	manager.now = func() time.Time { return now }
	data := newPayload()
	data.Values["user"] = "Rafa"

	first, err := manager.encodedCookie(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.encodedCookie(data)
	if err != nil {
		t.Fatal(err)
	}
	firstCookie, err := http.ParseSetCookie(first)
	if err != nil {
		t.Fatal(err)
	}
	secondCookie, err := http.ParseSetCookie(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstCookie.Value == secondCookie.Value {
		t.Fatal("identical session data reused an encryption nonce")
	}
	decoded, _, ok := manager.decode(firstCookie.Value)
	if !ok || decoded.Values["user"] != "Rafa" {
		t.Fatalf("decoded=%#v ok=%v", decoded, ok)
	}

	now = now.Add(time.Minute + time.Second)
	if _, _, ok := manager.decode(firstCookie.Value); ok {
		t.Fatal("expired session cookie was accepted")
	}
}
