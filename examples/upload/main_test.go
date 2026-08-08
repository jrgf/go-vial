package main

import (
	"net/http"
	"net/url"
	"path/filepath"
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

func TestUploadExampleMain(t *testing.T) {
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}

func TestUploadRequiresFile(t *testing.T) {
	server := testkit.Start(t, newApp())
	request := server.NewRequest(http.MethodPost, "/upload", strings.NewReader("title=missing"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Do(request).RequireStatus(http.StatusUnsupportedMediaType)
}

func TestUploadRejectsMalformedForm(t *testing.T) {
	server := testkit.Start(t, newApp())
	request := server.NewRequest(http.MethodPost, "/upload", strings.NewReader("title=%ZZ"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	server.Do(request).RequireStatus(http.StatusBadRequest)
}
