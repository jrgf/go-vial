package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpload(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("title", "Example"); err != nil {
		t.Fatalf("write title: %v", err)
	}
	file, err := writer.CreateFormFile("file", "example.txt")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := file.Write([]byte("hello vial")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/upload", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	newApp().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var result struct {
		Title    string `json:"title"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Title != "Example" || result.Filename != "example.txt" || result.Size != int64(len("hello vial")) {
		t.Fatalf("unexpected response %#v", result)
	}
}
