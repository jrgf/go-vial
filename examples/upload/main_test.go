package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jrgf/go-vial/testkit"
)

func TestUpload(t *testing.T) {
	server := testkit.Start(t, newApp())
	response := server.Multipart(
		http.MethodPost,
		"/upload",
		url.Values{"title": {"Example"}},
		testkit.File{Field: "file", Name: "example.txt", Body: strings.NewReader("hello vial")},
	)
	response.RequireStatus(http.StatusCreated)

	var result struct {
		Title    string `json:"title"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	response.Decode(&result)
	if result.Title != "Example" || result.Filename != "example.txt" || result.Size != int64(len("hello vial")) {
		t.Fatalf("unexpected response %#v", result)
	}
}
