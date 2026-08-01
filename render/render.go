package render

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"

	"github.com/jrgf/go-vial"
)

// Renderer executes a parsed template set. Templates must not be modified
// after the renderer starts serving requests.
type Renderer struct {
	templates *template.Template
}

// New creates a renderer from application-owned templates.
func New(templates *template.Template) *Renderer {
	if templates == nil {
		panic("vial/render: templates cannot be nil")
	}
	return &Renderer{templates: templates}
}

// HTML renders a named template before committing an HTML response.
func (renderer *Renderer) HTML(context *vial.Context, status int, name string, data any) error {
	if context.Committed() {
		return errors.New("vial/render: response already committed")
	}

	var body bytes.Buffer
	if err := renderer.templates.ExecuteTemplate(&body, name, data); err != nil {
		return fmt.Errorf("render template %q: %w", name, err)
	}

	response := context.Response()
	if response.Header().Get("Content-Type") == "" {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	response.WriteHeader(status)
	if _, err := response.Write(body.Bytes()); err != nil {
		return fmt.Errorf("write template %q: %w", name, err)
	}
	return nil
}
