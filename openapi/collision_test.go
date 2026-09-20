package openapi

import (
	"encoding/json"
	"net/http"
	"testing"

	vial "github.com/jrgf/go-vial"
)

func TestExactAndCatchAllRoutesCoexist(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		app := vial.New()
		exact := func() {
			app.Get("/", func(*vial.Context) error { return nil }, vial.RouteName("home"))
			app.Get("/files/{path}", func(*vial.Context) error { return nil })
		}
		catch := func() {
			app.HandleHTTP("GET /", http.NotFoundHandler())
			app.Get("/files/{path...}", func(*vial.Context) error { return nil })
		}
		if reverse {
			catch()
			exact()
		} else {
			exact()
			catch()
		}
		data, err := Generate(app, Config{Title: "test", Version: "1"})
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Paths map[string]map[string]struct {
				OperationID  string            `json:"operationId"`
				Alternatives []json.RawMessage `json:"x-vial-alternatives"`
			} `json:"paths"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Paths["/"]["get"].OperationID != "home" || len(doc.Paths["/"]["get"].Alternatives) != 1 || len(doc.Paths["/files/{path}"]["get"].Alternatives) != 1 {
			t.Fatalf("routes lost: %s", data)
		}
	}
}
