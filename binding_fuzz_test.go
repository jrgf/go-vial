package vial

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func FuzzSetScalar(fuzz *testing.F) {
	fuzz.Add(uint8(0), "value")
	fuzz.Add(uint8(1), "true")
	fuzz.Add(uint8(2), "-128")
	fuzz.Add(uint8(3), "255")
	fuzz.Add(uint8(4), "3.14")

	fuzz.Fuzz(func(t *testing.T, kind uint8, value string) {
		var destination any
		switch kind % 5 {
		case 0:
			destination = new(string)
		case 1:
			destination = new(bool)
		case 2:
			destination = new(int8)
		case 3:
			destination = new(uint8)
		case 4:
			destination = new(float32)
		}
		err := setScalar(reflect.ValueOf(destination).Elem(), value)
		if text, ok := destination.(*string); ok && (err != nil || *text != value) {
			t.Fatalf("string binding = %q, %v", *text, err)
		}
	})
}

func FuzzBindURLForm(fuzz *testing.F) {
	fuzz.Add("name=Ada&age=36&active=true&tag=go&tag=web")
	fuzz.Add("name=%zz")
	fuzz.Add("age=-1&active=maybe")

	fuzz.Fuzz(func(t *testing.T, body string) {
		if len(body) > 64<<10 {
			t.Skip()
		}

		app := New()
		app.Post("/", func(context *Context) error {
			var form struct {
				Name   string   `form:"name"`
				Age    uint8    `form:"age"`
				Active bool     `form:"active"`
				Tags   []string `form:"tag"`
			}
			if err := context.BindForm(&form); err != nil {
				return err
			}
			return context.NoContent(http.StatusNoContent)
		})

		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)

		if response.Code != http.StatusNoContent && response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected status %d", response.Code)
		}
	})
}

func FuzzBindJSON(fuzz *testing.F) {
	fuzz.Add(`{"name":"Ada","age":36}`)
	fuzz.Add(`{"name":`)
	fuzz.Add(`null`)

	fuzz.Fuzz(func(t *testing.T, body string) {
		if len(body) > 64<<10 {
			t.Skip()
		}
		app := New(WithDisallowUnknownJSONFields(true))
		app.Post("/", func(context *Context) error {
			var value struct {
				Name string `json:"name" validate:"required"`
				Age  uint8  `json:"age"`
			}
			if err := context.BindJSON(&value); err != nil {
				return err
			}
			return context.NoContent(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent && response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected status %d", response.Code)
		}
	})
}
