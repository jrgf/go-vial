package vial_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicPackagesFromExternalModule(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	root := filepath.Dir(source)
	directory := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/vial-compatibility

go 1.26.6

require github.com/jrgf/go-vial v0.0.0

replace github.com/jrgf/go-vial => %q
`, filepath.ToSlash(root))
	application := `package compatibility

import (
	"html/template"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/async"
	"github.com/jrgf/go-vial/auth"
	"github.com/jrgf/go-vial/config"
	"github.com/jrgf/go-vial/contrib/asyncpostgres"
	"github.com/jrgf/go-vial/fault"
	"github.com/jrgf/go-vial/middleware"
	"github.com/jrgf/go-vial/render"
	"github.com/jrgf/go-vial/session"
	"github.com/jrgf/go-vial/testkit"
)

func TestApplication(t *testing.T) {
	_ = async.NewMemoryExecutor
	_ = asyncpostgres.New

	var settings struct{ HTTP config.HTTP }
	if err := config.Load(&settings, config.Environ(nil)); err != nil {
		t.Fatal(err)
	}

	renderer := render.New(template.Must(template.New("page").Parse("{{define \"page\"}}hello{{end}}")))
	sessions, err := session.New(session.Config{
		Keys: [][]byte{[]byte("0123456789abcdef0123456789abcdef")},
		DangerouslyAllowInsecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := auth.New(auth.Config{
		Challenge: "Session",
		Authenticate: func(context *vial.Context) (auth.Identity, bool, error) {
			current, err := sessions.From(context)
			if err != nil {
				return auth.Identity{}, false, err
			}
			subject, ok := current.Get("external")
			return auth.Identity{Subject: subject}, ok, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	limit, err := middleware.RateLimit(middleware.RateLimitConfig{
		Requests: 100,
		Window:   time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(middleware.SecurityHeaders(), limit, sessions.Middleware(), identities.Middleware(), middleware.RequestID(), middleware.Recover())
	app.Get("/", func(context *vial.Context) error {
		return renderer.HTML(context, http.StatusOK, "page", nil)
	}, vial.RouteName("home"))
	app.Get("/fault", func(*vial.Context) error {
		return fault.New(fault.InvalidArgument, "invalid", "invalid request")
	})
	app.Get("/session", func(context *vial.Context) error {
		current, err := sessions.From(context)
		if err != nil {
			return err
		}
		if err := current.Set("external", "works"); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/authenticated", func(context *vial.Context) error {
		identity, _ := identities.From(context)
		return context.JSON(http.StatusOK, identity)
	}, vial.RouteMiddleware(identities.Require()))

	server := testkit.Start(t, app)
	response := server.Do(server.NewRequest(http.MethodGet, "/", nil))
	response.RequireStatus(http.StatusOK)
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers are missing")
	}
	if !strings.Contains(response.Text(), "hello") {
		t.Fatal("rendered response is missing content")
	}
	if route := testkit.RequireRoute(t, app, http.MethodGet, "/"); route.Name != "home" {
		t.Fatalf("route name = %q", route.Name)
	}
	failure := server.Do(server.NewRequest(http.MethodGet, "/fault", nil))
	failure.RequireStatus(http.StatusBadRequest)
	if got := failure.Fault().Code; got != "invalid" {
		t.Fatalf("fault code = %q", got)
	}
	sessionResponse := server.Do(server.NewRequest(http.MethodGet, "/session", nil))
	sessionResponse.RequireStatus(http.StatusNoContent)
	if len(sessionResponse.Cookies()) != 1 {
		t.Fatal("session cookie was not written")
	}
	authenticated := server.Do(server.NewRequest(http.MethodGet, "/authenticated", nil))
	authenticated.RequireStatus(http.StatusOK)
}
`

	for name, content := range map[string]string{"go.mod": goMod, "app_test.go": application} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external module test: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}
