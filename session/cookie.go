package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (manager *Manager) decode(value string) (payload, int, bool) {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(encoded) == 0 || encoded[0] != formatVersion {
		return newPayload(), -1, false
	}
	for index, aead := range manager.currentKeys() {
		if len(encoded) < 1+aead.NonceSize()+aead.Overhead() {
			continue
		}
		nonce := encoded[1 : 1+aead.NonceSize()]
		ciphertext := encoded[1+aead.NonceSize():]
		plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(manager.name))
		if err != nil {
			continue
		}
		decoded := newPayload()
		if err := json.Unmarshal(plaintext, &decoded); err != nil {
			continue
		}
		now := manager.now().Unix()
		if decoded.IssuedAt <= 0 || decoded.IssuedAt > now+clockSkewSeconds || decoded.IssuedAt <= now-int64(manager.maxAge/time.Second) {
			continue
		}
		if decoded.Values == nil {
			decoded.Values = make(map[string]string)
		}
		return decoded, index, true
	}
	return newPayload(), -1, false
}

func (manager *Manager) encodedCookie(data payload) (string, error) {
	if len(data.Values) == 0 && len(data.Flashes) == 0 {
		return manager.expiredCookie().String(), nil
	}
	now := manager.now()
	if data.IssuedAt == 0 {
		data.IssuedAt = now.Unix()
	}
	expires := time.Unix(data.IssuedAt, 0).Add(manager.maxAge)
	remaining := expires.Unix() - now.Unix()
	if remaining <= 0 {
		return manager.expiredCookie().String(), nil
	}
	value, err := manager.seal(data)
	if err != nil {
		return "", err
	}
	serialized := manager.cookie(value, int(remaining), expires).String()
	if serialized == "" {
		return "", errors.New("session: invalid cookie")
	}
	if len(serialized) > MaxCookieBytes {
		return "", ErrTooLarge
	}
	return serialized, nil
}

func (manager *Manager) seal(data payload) (string, error) {
	plaintext, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("session: encode cookie: %w", err)
	}
	aead := manager.currentKeys()[0]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("session: generate nonce: %w", err)
	}
	encoded := make([]byte, 1, 1+len(nonce)+len(plaintext)+aead.Overhead())
	encoded[0] = formatVersion
	encoded = append(encoded, nonce...)
	encoded = aead.Seal(encoded, nonce, plaintext, []byte(manager.name))
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func (manager *Manager) expiredCookie() *http.Cookie {
	return manager.cookie("", -1, time.Unix(1, 0))
}

func (manager *Manager) cookie(value string, maxAge int, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     manager.name,
		Value:    value,
		Path:     manager.path,
		Domain:   manager.domain,
		Expires:  expires,
		MaxAge:   maxAge,
		Secure:   manager.secure,
		HttpOnly: true,
		SameSite: manager.sameSite,
	}
}

func (manager *Manager) currentKeys() []cipher.AEAD {
	manager.mu.RLock()
	keys := manager.keys
	manager.mu.RUnlock()
	return keys
}

func deriveKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("go-vial/session/v1/encryption"))
	return mac.Sum(nil)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
