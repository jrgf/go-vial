package render_test

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/render"
)

func TestHTML(t *testing.T) {
	views := render.New(template.Must(template.New("views").Parse(
		`{{define "layout"}}<main>{{template "content" .}}</main>{{end}}{{define "content"}}{{.}}{{end}}`,
	)))
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		return views.HTML(context, http.StatusCreated, "layout", `<Vial>`)
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type=%q", got)
	}
	if got := response.Body.String(); got != "<main>&lt;Vial&gt;</main>" {
		t.Fatalf("body=%q", got)
	}
}

func TestHTMLPreservesContentType(t *testing.T) {
	views := render.New(template.Must(template.New("page").Parse("hello")))
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		context.Response().Header().Set("Content-Type", "application/xhtml+xml")
		return views.HTML(context, http.StatusOK, "page", nil)
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := response.Header().Get("Content-Type"); got != "application/xhtml+xml" {
		t.Fatalf("content type=%q", got)
	}
}

func TestHTMLErrorsDoNotCommitPartialTemplate(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		renderName string
		data       any
	}{
		{"missing template", `{{define "page"}}unused{{end}}`, "missing", nil},
		{"execution error", `{{define "broken"}}partial {{.Missing}}{{end}}`, "broken", map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := template.Must(template.New("views").Option("missingkey=error").Parse(test.template))
			views := render.New(parsed)
			var renderErr error
			app := vial.New()
			app.Get("/", func(context *vial.Context) error {
				renderErr = views.HTML(context, http.StatusOK, test.renderName, test.data)
				return renderErr
			})

			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

			if renderErr == nil || !strings.Contains(renderErr.Error(), `template "`+test.renderName+`"`) {
				t.Fatalf("error=%v", renderErr)
			}
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d", response.Code)
			}
			if strings.Contains(response.Body.String(), "partial") {
				t.Fatalf("partial template reached response: %q", response.Body.String())
			}
		})
	}
}

func TestHTMLRejectsCommittedResponse(t *testing.T) {
	views := render.New(template.Must(template.New("page").Parse("second")))
	var renderErr error
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		if err := context.Text(http.StatusOK, "first"); err != nil {
			return err
		}
		renderErr = views.HTML(context, http.StatusOK, "page", nil)
		return nil
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if renderErr == nil || renderErr.Error() != "vial/render: response already committed" {
		t.Fatalf("error=%v", renderErr)
	}
	if got := response.Body.String(); got != "first" {
		t.Fatalf("body=%q", got)
	}
}

func TestHTMLReturnsWriteError(t *testing.T) {
	views := render.New(template.Must(template.New("page").Parse("hello")))
	want := errors.New("write failed")
	writer := &errorWriter{header: make(http.Header), err: want}
	var renderErr error
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		renderErr = views.HTML(context, http.StatusOK, "page", nil)
		return nil
	})

	app.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(renderErr, want) {
		t.Fatalf("error=%v", renderErr)
	}
}

func TestNewRejectsNilTemplate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	render.New(nil)
}

type errorWriter struct {
	header http.Header
	err    error
}

func (writer *errorWriter) Header() http.Header {
	return writer.header
}

func (*errorWriter) WriteHeader(int) {}

func (writer *errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}
