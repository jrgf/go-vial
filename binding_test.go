package vial_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestBindQuery(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"valid", "q=vial&page=2&active=true&tag=go&tag=web&score=4.5", http.StatusOK},
		{"invalid", "page=nope", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			app.Get("/search", func(context *vial.Context) error {
				var query struct {
					Search string    `query:"q"`
					Page   int       `query:"page"`
					Active bool      `query:"active"`
					Tags   []string  `query:"tag"`
					Score  float64   `query:"score"`
					IDs    []uint16  `query:"id"`
					Skip   complex64 `query:"-" json:"-"`
				}
				if err := context.BindQuery(&query); err != nil {
					return err
				}
				return context.JSON(http.StatusOK, query)
			})

			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/search?"+test.query, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantStatus == http.StatusOK && !strings.Contains(response.Body.String(), `"Tags":["go","web"]`) {
				t.Fatalf("unexpected query response %s", response.Body.String())
			}
		})
	}
}

func TestBindCombinesRequestSources(t *testing.T) {
	type input struct {
		ID        uint64   `path:"id" json:"id"`
		Page      int      `query:"page" json:"page"`
		RequestID string   `header:"X-Request-ID" json:"request_id"`
		Roles     []string `header:"X-Role" json:"roles"`
		Session   string   `cookie:"session" json:"session"`
		Name      string   `json:"name"`
	}

	app := vial.New()
	app.Post("/items/{id}", func(context *vial.Context) error {
		var request input
		if err := context.Bind(&request); err != nil {
			return err
		}
		return context.JSON(http.StatusOK, request)
	})

	request := httptest.NewRequest(http.MethodPost, "/items/42?page=3", strings.NewReader(`{"name":"Vial"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-123")
	request.Header.Add("X-Role", "reader")
	request.Header.Add("X-Role", "writer")
	request.AddCookie(&http.Cookie{Name: "session", Value: "session-123"})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	var got input
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := input{
		ID:        42,
		Page:      3,
		RequestID: "request-123",
		Roles:     []string{"reader", "writer"},
		Session:   "session-123",
		Name:      "Vial",
	}
	if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("combined binding: status=%d payload=%#v, want %#v", response.Code, got, want)
	}
}

func TestBindCombinedContentTypes(t *testing.T) {
	type input struct {
		Page int    `query:"page"`
		Name string `form:"name"`
	}

	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
		wantStatus  int
		want        input
	}{
		{name: "no body", method: http.MethodGet, target: "/?page=2", wantStatus: http.StatusOK, want: input{Page: 2}},
		{name: "form", method: http.MethodPost, target: "/?page=2", body: "name=Vial", contentType: "application/x-www-form-urlencoded", wantStatus: http.StatusOK, want: input{Page: 2, Name: "Vial"}},
		{name: "invalid query", method: http.MethodGet, target: "/?page=bad", wantStatus: http.StatusBadRequest},
		{name: "unsupported", method: http.MethodPost, target: "/", body: "name=Vial", contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
		{name: "invalid content type", method: http.MethodPost, target: "/", body: "name=Vial", contentType: ";", wantStatus: http.StatusUnsupportedMediaType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			app.Handle(test.method, "/", func(context *vial.Context) error {
				var request input
				if err := context.Bind(&request); err != nil {
					return err
				}
				return context.JSON(http.StatusOK, request)
			})

			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantStatus == http.StatusOK {
				var got input
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("payload=%#v, want %#v", got, test.want)
				}
			}
		})
	}
}

func TestBindingFieldErrors(t *testing.T) {
	type input struct {
		ID    int `path:"id"`
		Page  int `query:"page"`
		Limit int `header:"X-Limit"`
		Count int `cookie:"count"`
		Age   int `form:"age"`
		Name  int `json:"name"`
	}

	tests := []struct {
		name        string
		method      string
		target      string
		body        string
		contentType string
		source      string
		field       string
		code        string
		configure   func(*http.Request)
		bind        func(*vial.Context, any) error
	}{
		{name: "path", method: http.MethodGet, target: "/items/bad", source: "path", field: "id", code: "invalid_path", bind: (*vial.Context).BindPath},
		{name: "query", method: http.MethodGet, target: "/items/1?page=bad", source: "query", field: "page", code: "invalid_query", bind: (*vial.Context).BindQuery},
		{name: "header", method: http.MethodGet, target: "/items/1", source: "header", field: "X-Limit", code: "invalid_header", configure: func(request *http.Request) {
			request.Header.Set("X-Limit", "bad")
		}, bind: (*vial.Context).BindHeader},
		{name: "cookie", method: http.MethodGet, target: "/items/1", source: "cookie", field: "count", code: "invalid_cookie", configure: func(request *http.Request) {
			request.AddCookie(&http.Cookie{Name: "count", Value: "bad"})
		}, bind: (*vial.Context).BindCookie},
		{name: "form", method: http.MethodPost, target: "/items/1", body: "age=bad", contentType: "application/x-www-form-urlencoded", source: "form", field: "age", code: "invalid_form", bind: (*vial.Context).BindForm},
		{name: "json", method: http.MethodPost, target: "/items/1", body: `{"name":"bad"}`, contentType: "application/json", source: "json", field: "name", code: "invalid_json", bind: (*vial.Context).BindJSON},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			var fieldErr *vial.BindingError
			app.Handle(test.method, "/items/{id}", func(context *vial.Context) error {
				var request input
				err := test.bind(context, &request)
				if !errors.As(err, &fieldErr) {
					t.Fatalf("binding error %v does not contain BindingError", err)
				}
				return err
			})

			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.configure != nil {
				test.configure(request)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest || fieldErr.Source != test.source || fieldErr.Field != test.field {
				t.Fatalf("unexpected field error: status=%d error=%#v body=%s", response.Code, fieldErr, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || !strings.Contains(response.Body.String(), `"`+test.field+`":"invalid value"`) {
				t.Fatalf("unexpected binding response: %s", response.Body.String())
			}
		})
	}
}

func TestBindingErrorRetainsCause(t *testing.T) {
	cause := errors.New("invalid value")
	err := &vial.BindingError{Source: "query", Field: "page", Cause: cause}
	if !errors.Is(err, cause) || err.Error() != `query field "page": invalid value` {
		t.Fatalf("unexpected binding error: %v", err)
	}
	if (*vial.BindingError)(nil).Error() != "<nil>" || (*vial.BindingError)(nil).Unwrap() != nil {
		t.Fatal("nil binding error methods returned unexpected values")
	}
}

func TestBindingRejectsUnsupportedFieldType(t *testing.T) {
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		var request struct {
			Value complex64 `query:"value"`
		}
		return context.BindQuery(&request)
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?value=1", nil))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"value":"invalid value"`) {
		t.Fatalf("unexpected unsupported field response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBindURLForm(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
	}{
		{"valid", "name=Ada&age=36&active=true&tag=go&tag=web", "application/x-www-form-urlencoded", http.StatusOK},
		{"invalid", "age=nope", "application/x-www-form-urlencoded", http.StatusBadRequest},
		{"unsupported", "name=Ada", "text/plain", http.StatusUnsupportedMediaType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			app.Post("/people", func(context *vial.Context) error {
				var form struct {
					Name   string   `form:"name"`
					Age    uint8    `form:"age"`
					Active bool     `form:"active"`
					Tags   []string `form:"tag"`
				}
				if err := context.BindForm(&form); err != nil {
					return err
				}
				return context.JSON(http.StatusOK, form)
			})

			request := httptest.NewRequest(http.MethodPost, "/people", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBindMultipartAndFormFile(t *testing.T) {
	body, contentType := multipartBody(t, map[string][]string{
		"title": {"Release"},
		"tag":   {"go", "web"},
	}, "upload", "note.txt", []byte("hello vial"))

	app := vial.New()
	app.Post("/upload", func(context *vial.Context) error {
		var form struct {
			Title string   `form:"title"`
			Tags  []string `form:"tag"`
		}
		if err := context.BindForm(&form); err != nil {
			return err
		}
		file, header, err := context.FormFile("upload")
		if err != nil {
			return err
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		return context.JSON(http.StatusOK, map[string]any{
			"title":    form.Title,
			"tags":     form.Tags,
			"filename": header.Filename,
			"content":  string(content),
		})
	})

	request := httptest.NewRequest(http.MethodPost, "/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["title"] != "Release" || result["filename"] != "note.txt" || result["content"] != "hello vial" {
		t.Fatalf("unexpected upload response %#v", result)
	}
}

func TestMultipartErrors(t *testing.T) {
	tests := []struct {
		name       string
		app        *vial.App
		request    *http.Request
		wantStatus int
		wantCode   string
	}{
		{
			name:       "malformed",
			app:        uploadApp(),
			request:    multipartRequest(http.MethodPost, "/upload", strings.NewReader("broken"), "multipart/form-data; boundary=test"),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_multipart",
		},
	}

	body, contentType := multipartBody(t, nil, "upload", "large.txt", bytes.Repeat([]byte("x"), 1024))
	tests = append(tests, struct {
		name       string
		app        *vial.App
		request    *http.Request
		wantStatus int
		wantCode   string
	}{
		name:       "too large",
		app:        uploadApp(vial.WithMaxBodySize(128)),
		request:    multipartRequest(http.MethodPost, "/upload", body, contentType),
		wantStatus: http.StatusRequestEntityTooLarge,
		wantCode:   "request_body_too_large",
	})
	missingBody, missingContentType := multipartBody(t, nil, "", "", nil)
	tests = append(tests, struct {
		name       string
		app        *vial.App
		request    *http.Request
		wantStatus int
		wantCode   string
	}{
		name:       "missing file",
		app:        uploadApp(),
		request:    multipartRequest(http.MethodPost, "/upload", missingBody, missingContentType),
		wantStatus: http.StatusBadRequest,
		wantCode:   "missing_file",
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.app.ServeHTTP(response, test.request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMultipartTemporaryFileIsRemoved(t *testing.T) {
	body, contentType := multipartBody(
		t,
		nil,
		"upload",
		"large.txt",
		bytes.Repeat([]byte("x"), (1<<20)+1),
	)

	var temporaryPath string
	app := vial.New()
	app.Post("/upload", func(context *vial.Context) error {
		file, _, err := context.FormFile("upload")
		if err != nil {
			return err
		}
		diskFile, ok := file.(*os.File)
		if !ok {
			_ = file.Close()
			return vial.InternalServerError(errors.New("upload did not spill to disk"))
		}
		temporaryPath = diskFile.Name()
		if err := diskFile.Close(); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/upload", body)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if temporaryPath == "" {
		t.Fatal("multipart file did not spill to disk")
	}
	if _, err := os.Stat(temporaryPath); !os.IsNotExist(err) {
		t.Fatalf("temporary upload still exists: %s", temporaryPath)
	}
}

func uploadApp(options ...vial.Option) *vial.App {
	app := vial.New(options...)
	app.Post("/upload", func(context *vial.Context) error {
		file, _, err := context.FormFile("upload")
		if err != nil {
			return err
		}
		defer file.Close()
		return context.NoContent(http.StatusNoContent)
	})
	return app
}

func multipartBody(t *testing.T, fields map[string][]string, fileField, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for name, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatalf("write field: %v", err)
			}
		}
	}
	if fileField != "" {
		file, err := writer.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatalf("create file: %v", err)
		}
		if _, err := file.Write(content); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

func multipartRequest(method, target string, body io.Reader, contentType string) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", contentType)
	return request
}
